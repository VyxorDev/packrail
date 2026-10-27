package manifest

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/VyxorDev/packrail/internal/registry"
)

const (
	FileName       = "packrail.json"
	CurrentVersion = 1
	maxFileBytes   = 1 << 20
)

var packageName = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

type Dependency struct {
	Constraint string `json:"constraint"`
	Repo       string `json:"repo,omitempty"`
}

type File struct {
	ManifestVersion int                   `json:"manifest_version"`
	Dependencies    map[string]Dependency `json:"dependencies"`
}

func New() File {
	return File{ManifestVersion: CurrentVersion, Dependencies: map[string]Dependency{}}
}

func Path() (string, error) {
	return filepath.Abs(FileName)
}

func Load(path string) (File, error) {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return File{}, fmt.Errorf("manifest %s not found; run 'prl init' to create it: %w", path, err)
		}
		return File{}, fmt.Errorf("stat manifest %s: %w", path, err)
	}
	if info.IsDir() {
		return File{}, fmt.Errorf("manifest path %s is a directory", path)
	}
	if info.Size() > maxFileBytes {
		return File{}, fmt.Errorf("manifest %s exceeds %d bytes", path, maxFileBytes)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return File{}, fmt.Errorf("read manifest %s: %w", path, err)
	}
	file, err := Decode(data)
	if err != nil {
		return File{}, fmt.Errorf("decode manifest %s: %w", path, err)
	}
	return file, nil
}

func LoadOrNew(path string) (File, bool, error) {
	file, err := Load(path)
	if err == nil {
		return file, true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return New(), false, nil
	}
	return File{}, false, err
}

func Decode(data []byte) (File, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var raw struct {
		ManifestVersion int                        `json:"manifest_version"`
		Dependencies    map[string]json.RawMessage `json:"dependencies"`
	}
	if err := dec.Decode(&raw); err != nil {
		return File{}, err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return File{}, fmt.Errorf("manifest contains trailing JSON values")
		}
		return File{}, fmt.Errorf("trailing manifest data: %w", err)
	}
	file := File{ManifestVersion: raw.ManifestVersion, Dependencies: map[string]Dependency{}}
	for name, value := range raw.Dependencies {
		dep, err := decodeDependency(value)
		if err != nil {
			return File{}, fmt.Errorf("dependency %s: %w", name, err)
		}
		file.Dependencies[name] = dep
	}
	if err := Validate(&file); err != nil {
		return File{}, err
	}
	return file, nil
}

func decodeDependency(data []byte) (Dependency, error) {
	var shorthand string
	if err := json.Unmarshal(data, &shorthand); err == nil {
		return Dependency{Constraint: shorthand}, nil
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var dep Dependency
	if err := dec.Decode(&dep); err != nil {
		return Dependency{}, fmt.Errorf("expected a version string or dependency object: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return Dependency{}, fmt.Errorf("dependency object contains trailing JSON values")
		}
		return Dependency{}, err
	}
	return dep, nil
}

func Validate(file *File) error {
	if file == nil {
		return fmt.Errorf("manifest is nil")
	}
	if file.ManifestVersion != CurrentVersion {
		return fmt.Errorf("unsupported manifest_version %d (supported: %d)", file.ManifestVersion, CurrentVersion)
	}
	if file.Dependencies == nil {
		file.Dependencies = map[string]Dependency{}
	}
	normalized := make(map[string]Dependency, len(file.Dependencies))
	for rawName, dep := range file.Dependencies {
		name := strings.ToLower(strings.TrimSpace(rawName))
		if !packageName.MatchString(name) {
			return fmt.Errorf("invalid dependency name %q", rawName)
		}
		if _, exists := normalized[name]; exists {
			return fmt.Errorf("duplicate dependency name %q", name)
		}
		dep.Constraint = strings.TrimSpace(dep.Constraint)
		if dep.Constraint == "" {
			dep.Constraint = "*"
		}
		if _, _, err := registry.ParseDependencyTarget(name + "@" + dep.Constraint); err != nil {
			return fmt.Errorf("dependency %s: %w", name, err)
		}
		dep.Repo = normalizeRepo(dep.Repo)
		if dep.Repo != "" {
			if err := validateRepo(dep.Repo); err != nil {
				return fmt.Errorf("dependency %s has invalid GitHub repo %q: %w", name, dep.Repo, err)
			}
		}
		normalized[name] = dep
	}
	file.Dependencies = normalized
	return nil
}

func Upsert(file *File, name string, dep Dependency) error {
	if file == nil {
		return fmt.Errorf("manifest is nil")
	}
	clone := Clone(*file)
	if clone.ManifestVersion == 0 {
		clone.ManifestVersion = CurrentVersion
	}
	if clone.Dependencies == nil {
		clone.Dependencies = map[string]Dependency{}
	}
	clone.Dependencies[strings.ToLower(strings.TrimSpace(name))] = dep
	if err := Validate(&clone); err != nil {
		return err
	}
	*file = clone
	return nil
}

func Remove(file *File, name string) (bool, error) {
	if file == nil {
		return false, fmt.Errorf("manifest is nil")
	}
	clone := Clone(*file)
	if err := Validate(&clone); err != nil {
		return false, err
	}
	key := strings.ToLower(strings.TrimSpace(name))
	if _, ok := clone.Dependencies[key]; !ok {
		return false, nil
	}
	delete(clone.Dependencies, key)
	*file = clone
	return true, nil
}

func Clone(file File) File {
	out := File{ManifestVersion: file.ManifestVersion, Dependencies: make(map[string]Dependency, len(file.Dependencies))}
	for name, dep := range file.Dependencies {
		out.Dependencies[name] = dep
	}
	return out
}

func Encode(file File) ([]byte, error) {
	copy := Clone(file)
	if err := Validate(&copy); err != nil {
		return nil, err
	}
	// encoding/json sorts map keys, giving deterministic dependency ordering.
	data, err := json.MarshalIndent(copy, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func Save(path string, file File) error {
	data, err := Encode(file)
	if err != nil {
		return fmt.Errorf("validate manifest before writing: %w", err)
	}
	return writeAtomic(path, data)
}

func Names(file File) []string {
	names := make([]string, 0, len(file.Dependencies))
	for name := range file.Dependencies {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create manifest directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".packrail-manifest-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary manifest: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err == nil {
		return nil
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return os.Rename(tmpPath, path)
}

func normalizeRepo(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimRight(value, "/")
	value = strings.TrimSuffix(value, ".git")
	if strings.HasPrefix(strings.ToLower(value), "https://github.com/") {
		value = value[len("https://github.com/"):]
	}
	return strings.Trim(value, "/")
}

func validateRepo(value string) error {
	parts := strings.Split(value, "/")
	if len(parts) != 2 {
		return fmt.Errorf("expected owner/repo")
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return fmt.Errorf("owner and repository must be non-empty names")
		}
		if len(part) > 100 {
			return fmt.Errorf("owner or repository name is too long")
		}
		for _, r := range part {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
				continue
			}
			return fmt.Errorf("owner or repository contains invalid character %q", r)
		}
	}
	return nil
}
