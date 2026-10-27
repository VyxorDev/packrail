package lockfile

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/VyxorDev/packrail/internal/registry"
	packruntimes "github.com/VyxorDev/packrail/internal/runtimes"
)

const (
	FileName       = "packrail.lock"
	CurrentVersion = 2
	maxFileBytes   = 16 << 20
)

// File is a self-contained set of resolved package definitions. Version 2 also
// pins exact managed-runtime artifacts per platform. Version 1 remains readable
// and keeps its historical constraint-based runtime behavior until it is
// rewritten by `prl add`.
type File struct {
	LockfileVersion int                                   `json:"lockfile_version"`
	Packages        []registry.Package                    `json:"packages"`
	Runtimes        map[string]packruntimes.LockedRuntime `json:"runtimes,omitempty"`
}

func New() File {
	return File{
		LockfileVersion: CurrentVersion,
		Packages:        []registry.Package{},
		Runtimes:        map[string]packruntimes.LockedRuntime{},
	}
}

// Path returns the lockfile in the current working directory.
func Path() (string, error) {
	return filepath.Abs(FileName)
}

func LoadOrEmpty(path string) (File, bool, error) {
	file, err := Load(path)
	if err == nil {
		return file, true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		file = New()
		packruntimes.ConfigureLockedRuntimes(file.Runtimes)
		return file, false, nil
	}
	return File{}, false, err
}

func Load(path string) (File, error) {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return File{}, fmt.Errorf("lockfile %s not found; run 'prl add <package>' to create it: %w", path, err)
		}
		return File{}, fmt.Errorf("stat lockfile %s: %w", path, err)
	}
	if info.IsDir() {
		return File{}, fmt.Errorf("lockfile path %s is a directory", path)
	}
	if info.Size() > maxFileBytes {
		return File{}, fmt.Errorf("lockfile %s exceeds %d bytes", path, maxFileBytes)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return File{}, fmt.Errorf("read lockfile %s: %w", path, err)
	}
	var file File
	if err := json.Unmarshal(data, &file); err != nil {
		return File{}, fmt.Errorf("decode lockfile %s: %w", path, err)
	}
	if err := normalizeAndValidate(&file); err != nil {
		return File{}, fmt.Errorf("validate lockfile %s: %w", path, err)
	}
	packruntimes.ConfigureLockedRuntimes(file.Runtimes)
	return file, nil
}

// Upsert adds a package or replaces its existing entry by case-insensitive name.
// Rewriting a v1 lockfile upgrades it to v2 and resolves exact runtime pins for
// every managed-runtime package in the resulting project.
func Upsert(file *File, pkg registry.Package) error {
	if file == nil {
		return fmt.Errorf("lockfile is nil")
	}
	if file.LockfileVersion == 0 {
		file.LockfileVersion = CurrentVersion
	}
	if file.Packages == nil {
		file.Packages = []registry.Package{}
	}
	if err := normalizeAndValidate(file); err != nil {
		return err
	}
	pkg.Name = strings.ToLower(strings.TrimSpace(pkg.Name))
	if err := validatePackage(pkg); err != nil {
		return err
	}

	replaced := false
	for i, existing := range file.Packages {
		if strings.EqualFold(existing.Name, pkg.Name) {
			file.Packages[i] = pkg
			replaced = true
			break
		}
	}
	if !replaced {
		file.Packages = append(file.Packages, pkg)
	}

	file.LockfileVersion = CurrentVersion
	if file.Runtimes == nil {
		file.Runtimes = map[string]packruntimes.LockedRuntime{}
	}
	if err := resolveRuntimePins(file); err != nil {
		return err
	}
	if err := normalizeAndValidate(file); err != nil {
		return err
	}
	packruntimes.ConfigureLockedRuntimes(file.Runtimes)
	return nil
}

// Remove deletes a package entry from the project lockfile. It deliberately
// does not uninstall the package from PACKRAIL_HOME because that installation
// may be shared by other projects. Unused v2 runtime pins are pruned.
func Remove(file *File, name string) (bool, error) {
	if file == nil {
		return false, fmt.Errorf("lockfile is nil")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return false, fmt.Errorf("package name is required")
	}
	if err := normalizeAndValidate(file); err != nil {
		return false, err
	}
	for i, pkg := range file.Packages {
		if strings.EqualFold(pkg.Name, name) {
			file.Packages = append(file.Packages[:i], file.Packages[i+1:]...)
			if len(file.Packages) == 0 {
				file.Packages = []registry.Package{}
			}
			if file.LockfileVersion == CurrentVersion {
				pruneRuntimePins(file)
				packruntimes.ConfigureLockedRuntimes(file.Runtimes)
			}
			return true, nil
		}
	}
	return false, nil
}

func Save(path string, file File) error {
	file.Packages = append([]registry.Package(nil), file.Packages...)
	if file.Runtimes == nil && file.LockfileVersion == CurrentVersion {
		file.Runtimes = map[string]packruntimes.LockedRuntime{}
	}
	if err := normalizeAndValidate(&file); err != nil {
		return fmt.Errorf("validate lockfile before writing: %w", err)
	}
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return fmt.Errorf("encode lockfile: %w", err)
	}
	data = append(data, '\n')

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create lockfile directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".packrail-lock-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary lockfile: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return fmt.Errorf("set lockfile permissions: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write lockfile: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync lockfile: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close lockfile: %w", err)
	}
	if err := replaceFile(tmpPath, path); err != nil {
		return fmt.Errorf("commit lockfile: %w", err)
	}
	packruntimes.ConfigureLockedRuntimes(file.Runtimes)
	return nil
}

func validatePackage(pkg registry.Package) error {
	if err := registry.ValidatePackage(pkg); err != nil {
		return fmt.Errorf("package %s: %w", pkg.Name, err)
	}
	if err := registry.ValidateGitHubProvenance(pkg); err != nil {
		return fmt.Errorf("package %s provenance: %w", pkg.Name, err)
	}
	return nil
}

func normalizeAndValidate(file *File) error {
	if file == nil {
		return fmt.Errorf("lockfile is nil")
	}
	if file.LockfileVersion != 1 && file.LockfileVersion != CurrentVersion {
		return fmt.Errorf("unsupported lockfile_version %d (supported: 1, %d)", file.LockfileVersion, CurrentVersion)
	}
	if file.Packages == nil {
		file.Packages = []registry.Package{}
	}
	seen := make(map[string]struct{}, len(file.Packages))
	for i := range file.Packages {
		pkg := &file.Packages[i]
		pkg.Name = strings.ToLower(strings.TrimSpace(pkg.Name))
		key := pkg.Name
		if key == "" {
			return fmt.Errorf("package %d has no name", i)
		}
		if _, exists := seen[key]; exists {
			return fmt.Errorf("duplicate package name %q", pkg.Name)
		}
		seen[key] = struct{}{}
		if err := validatePackage(*pkg); err != nil {
			return err
		}
	}
	sort.Slice(file.Packages, func(i, j int) bool {
		return file.Packages[i].Name < file.Packages[j].Name
	})

	if file.LockfileVersion == 1 {
		file.Runtimes = nil
		return nil
	}
	if file.Runtimes == nil {
		file.Runtimes = map[string]packruntimes.LockedRuntime{}
	}
	for _, pkg := range file.Packages {
		if strings.TrimSpace(pkg.Runtime.Name) == "" {
			continue
		}
		key := packruntimes.SpecKey(pkg.Runtime)
		locked, ok := file.Runtimes[key]
		if !ok {
			return fmt.Errorf("package %s runtime %s is not pinned", pkg.Name, pkg.Runtime.Name)
		}
		if !sameRuntimeSpec(pkg.Runtime, locked) {
			return fmt.Errorf("package %s runtime pin does not match its registry constraint", pkg.Name)
		}
		if err := packruntimes.ValidateLockedRuntime(locked, runtimePlatformsForPackage(pkg)); err != nil {
			return fmt.Errorf("package %s runtime pins: %w", pkg.Name, err)
		}
	}
	return nil
}

func resolveRuntimePins(file *File) error {
	needed := map[string]registry.Runtime{}
	platforms := map[string]map[string]struct{}{}
	for _, pkg := range file.Packages {
		if strings.TrimSpace(pkg.Runtime.Name) == "" {
			continue
		}
		key := packruntimes.SpecKey(pkg.Runtime)
		needed[key] = pkg.Runtime
		if platforms[key] == nil {
			platforms[key] = map[string]struct{}{}
		}
		for _, platform := range runtimePlatformsForPackage(pkg) {
			platforms[key][platform] = struct{}{}
		}
	}

	for key, spec := range needed {
		required := make([]string, 0, len(platforms[key]))
		for platform := range platforms[key] {
			required = append(required, platform)
		}
		sort.Strings(required)
		if existing, ok := file.Runtimes[key]; ok && sameRuntimeSpec(spec, existing) && packruntimes.ValidateLockedRuntime(existing, required) == nil {
			continue
		}
		locked, err := packruntimes.ResolveLockedRuntime(spec, required)
		if err != nil {
			return fmt.Errorf("pin runtime %s %s: %w", spec.Name, spec.Constraint, err)
		}
		file.Runtimes[key] = locked
	}
	pruneRuntimePins(file)
	return nil
}

func pruneRuntimePins(file *File) {
	if file.Runtimes == nil {
		return
	}
	used := map[string]bool{}
	for _, pkg := range file.Packages {
		if strings.TrimSpace(pkg.Runtime.Name) != "" {
			used[packruntimes.SpecKey(pkg.Runtime)] = true
		}
	}
	for key := range file.Runtimes {
		if !used[key] {
			delete(file.Runtimes, key)
		}
	}
}

func runtimePlatformsForPackage(pkg registry.Package) []string {
	all := false
	set := map[string]struct{}{}
	for platform := range pkg.Artifacts {
		platform = strings.ToLower(strings.TrimSpace(platform))
		if platform == "any" {
			all = true
			break
		}
		if platform != "" {
			set[platform] = struct{}{}
		}
	}
	if all {
		return packruntimes.ManagedPlatforms()
	}
	out := make([]string, 0, len(set))
	for platform := range set {
		out = append(out, platform)
	}
	sort.Strings(out)
	return out
}

func sameRuntimeSpec(spec registry.Runtime, locked packruntimes.LockedRuntime) bool {
	name := strings.ToLower(strings.TrimSpace(spec.Name))
	lockedName := strings.ToLower(strings.TrimSpace(locked.Name))
	nameMatches := name == lockedName ||
		(name == "nodejs" && lockedName == "node") ||
		(name == "python3" && lockedName == "python") ||
		(name == "jre" && lockedName == "java") ||
		(name == ".net" && lockedName == "dotnet")
	return nameMatches &&
		strings.TrimSpace(spec.Constraint) == strings.TrimSpace(locked.Constraint) &&
		strings.TrimSpace(spec.Command) == strings.TrimSpace(locked.Command)
}

func replaceFile(source, destination string) error {
	if err := os.Rename(source, destination); err == nil {
		return nil
	} else {
		renameErr := err
		if _, statErr := os.Stat(destination); statErr == nil {
			if removeErr := os.Remove(destination); removeErr != nil {
				return fmt.Errorf("%v (remove existing lockfile: %w)", renameErr, removeErr)
			}
			return os.Rename(source, destination)
		}
		return renameErr
	}
}
