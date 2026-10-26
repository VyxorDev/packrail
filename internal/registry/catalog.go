package registry

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
)

// ManifestPath returns the canonical path for one package manifest.
func ManifestPath(dir, name string) string {
	return filepath.Join(dir, strings.ToLower(strings.TrimSpace(name))+".json")
}

// ReadManifest loads and validates one package manifest.
func ReadManifest(path string) (Package, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Package{}, fmt.Errorf("read manifest %s: %w", path, err)
	}
	var pkg Package
	if err := json.Unmarshal(data, &pkg); err != nil {
		return Package{}, fmt.Errorf("decode manifest %s: %w", path, err)
	}
	if err := ValidatePackage(pkg); err != nil {
		return Package{}, fmt.Errorf("validate manifest %s: %w", path, err)
	}
	return pkg, nil
}

// WriteManifest writes one validated package to <dir>/<name>.json.
func WriteManifest(dir string, pkg Package) (string, error) {
	if err := ValidatePackage(pkg); err != nil {
		return "", fmt.Errorf("validate package manifest: %w", err)
	}
	if strings.TrimSpace(dir) == "" {
		return "", fmt.Errorf("manifest directory is required")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create manifest directory: %w", err)
	}
	data, err := EncodePackage(pkg)
	if err != nil {
		return "", err
	}
	path := ManifestPath(dir, pkg.Name)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", fmt.Errorf("write manifest %s: %w", path, err)
	}
	return path, nil
}

// LoadCatalog reads all *.json package manifests in dir and validates the set.
func LoadCatalog(dir string) ([]Package, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read manifest directory %s: %w", dir, err)
	}
	var packages []Package
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".json") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		pkg, err := ReadManifest(path)
		if err != nil {
			return nil, err
		}
		expected := strings.ToLower(pkg.Name) + ".json"
		if entry.Name() != expected {
			return nil, fmt.Errorf("manifest %s must be named %s", entry.Name(), expected)
		}
		packages = append(packages, pkg)
	}
	if len(packages) == 0 {
		return nil, fmt.Errorf("manifest directory %s contains no packages", dir)
	}
	if err := ValidatePackages(packages); err != nil {
		return nil, fmt.Errorf("validate catalog: %w", err)
	}
	sort.Slice(packages, func(i, j int) bool {
		return strings.ToLower(packages[i].Name) < strings.ToLower(packages[j].Name)
	})
	return packages, nil
}

// EncodePackage returns stable human-readable JSON for a single manifest.
func EncodePackage(pkg Package) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(pkg); err != nil {
		return nil, fmt.Errorf("encode package manifest: %w", err)
	}
	return buffer.Bytes(), nil
}

// EncodeIndex returns the flattened registry index consumed by prl clients.
func EncodeIndex(packages []Package) ([]byte, error) {
	if err := ValidatePackages(packages); err != nil {
		return nil, err
	}
	copyPackages := append([]Package(nil), packages...)
	sort.Slice(copyPackages, func(i, j int) bool {
		return strings.ToLower(copyPackages[i].Name) < strings.ToLower(copyPackages[j].Name)
	})
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(copyPackages); err != nil {
		return nil, fmt.Errorf("encode registry index: %w", err)
	}
	return buffer.Bytes(), nil
}

// BuildIndex rebuilds indexPath from the canonical per-package manifests.
func BuildIndex(manifestDir, indexPath string) (int, error) {
	packages, err := LoadCatalog(manifestDir)
	if err != nil {
		return 0, err
	}
	data, err := EncodeIndex(packages)
	if err != nil {
		return 0, err
	}
	if err := os.MkdirAll(filepath.Dir(indexPath), 0o755); err != nil && filepath.Dir(indexPath) != "." {
		return 0, fmt.Errorf("create index directory: %w", err)
	}
	if err := os.WriteFile(indexPath, data, 0o644); err != nil {
		return 0, fmt.Errorf("write registry index %s: %w", indexPath, err)
	}
	return len(packages), nil
}

// ValidateIndex verifies that indexPath is exactly the deterministic flattening
// of manifestDir. This keeps hand-edited or stale indexes out of main.
func ValidateIndex(manifestDir, indexPath string) (int, error) {
	packages, err := LoadCatalog(manifestDir)
	if err != nil {
		return 0, err
	}
	expected, err := EncodeIndex(packages)
	if err != nil {
		return 0, err
	}
	actual, err := os.ReadFile(indexPath)
	if err != nil {
		return 0, fmt.Errorf("read registry index %s: %w", indexPath, err)
	}
	if !bytes.Equal(bytes.TrimSpace(actual), bytes.TrimSpace(expected)) {
		return 0, fmt.Errorf("registry index is stale; run 'prl registry build'")
	}
	return len(packages), nil
}

// ValidateMutation protects package identity and immutable published versions.
// A package name already bound to one source repository cannot silently move to
// another repository. Reusing a version with different metadata is also
// rejected; publish a new upstream version instead.
func ValidateMutation(oldPkg, newPkg Package) error {
	if !strings.EqualFold(strings.TrimSuffix(strings.TrimSpace(oldPkg.Source), "/"), strings.TrimSuffix(strings.TrimSpace(newPkg.Source), "/")) {
		return fmt.Errorf("package %s is already bound to %s; source cannot change to %s", oldPkg.Name, oldPkg.Source, newPkg.Source)
	}
	if strings.TrimSpace(oldPkg.Version) == strings.TrimSpace(newPkg.Version) && !reflect.DeepEqual(oldPkg, newPkg) {
		return fmt.Errorf("package %s version %s already exists with different metadata; published versions are immutable", oldPkg.Name, oldPkg.Version)
	}
	return nil
}
