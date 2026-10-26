package publisher

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/VyxorDev/packrail/internal/registry"
)

// UpsertRegistryFile validates pkg and writes it into a Packrail registry JSON
// file. Existing entries with the same package name are replaced; other
// packages are preserved. The result is sorted for stable diffs.
func UpsertRegistryFile(path string, pkg registry.Package) (bool, error) {
	if strings.TrimSpace(path) == "" {
		return false, fmt.Errorf("registry file path is required")
	}
	if err := registry.ValidatePackage(pkg); err != nil {
		return false, fmt.Errorf("validate generated package: %w", err)
	}

	var packages []registry.Package
	data, err := os.ReadFile(path)
	if err != nil {
		return false, fmt.Errorf("read registry file %s: %w", path, err)
	}
	if err := json.Unmarshal(data, &packages); err != nil {
		return false, fmt.Errorf("decode registry file %s: %w", path, err)
	}

	replaced := false
	for i := range packages {
		if strings.EqualFold(strings.TrimSpace(packages[i].Name), strings.TrimSpace(pkg.Name)) {
			packages[i] = pkg
			replaced = true
			break
		}
	}
	if !replaced {
		packages = append(packages, pkg)
	}
	if err := registry.ValidatePackages(packages); err != nil {
		return false, fmt.Errorf("validate updated registry: %w", err)
	}
	sort.Slice(packages, func(i, j int) bool {
		return strings.ToLower(packages[i].Name) < strings.ToLower(packages[j].Name)
	})

	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(packages); err != nil {
		return false, fmt.Errorf("encode updated registry: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil && filepath.Dir(path) != "." {
		return false, fmt.Errorf("create registry directory: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".packrail-registry-*.tmp")
	if err != nil {
		return false, fmt.Errorf("create registry staging file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.Write(buffer.Bytes()); err != nil {
		tmp.Close()
		return false, fmt.Errorf("write registry staging file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return false, fmt.Errorf("sync registry staging file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return false, fmt.Errorf("close registry staging file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		// Windows cannot rename over an existing file. Commit only after the
		// staged file has been fully written and validated.
		if removeErr := os.Remove(path); removeErr != nil {
			return false, fmt.Errorf("replace registry file: %w", err)
		}
		if renameErr := os.Rename(tmpPath, path); renameErr != nil {
			return false, fmt.Errorf("commit registry file: %w", renameErr)
		}
	}
	return replaced, nil
}
