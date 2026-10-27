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
)

const (
	FileName       = "packrail.lock"
	CurrentVersion = 1
	maxFileBytes   = 16 << 20
)

// File is a self-contained set of resolved package definitions. Keeping the
// complete metadata pins release URLs, platform artifacts, entrypoints, and
// SHA-256 checksums independently of future registry changes.
type File struct {
	LockfileVersion int               `json:"lockfile_version"`
	Packages        []registry.Package `json:"packages"`
}

func New() File {
	return File{LockfileVersion: CurrentVersion, Packages: []registry.Package{}}
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
		return New(), false, nil
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
	return file, nil
}

// Upsert adds a package or replaces its existing entry by case-insensitive name.
// The resulting package list is normalized and sorted for stable diffs.
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
	for i, existing := range file.Packages {
		if strings.EqualFold(existing.Name, pkg.Name) {
			file.Packages[i] = pkg
			return normalizeAndValidate(file)
		}
	}
	file.Packages = append(file.Packages, pkg)
	return normalizeAndValidate(file)
}

func Save(path string, file File) error {
	file.Packages = append([]registry.Package(nil), file.Packages...)
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
	if file.LockfileVersion != CurrentVersion {
		return fmt.Errorf("unsupported lockfile_version %d (supported: %d)", file.LockfileVersion, CurrentVersion)
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
	return nil
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
