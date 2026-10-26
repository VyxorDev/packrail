package registry

import (
	"embed"
	"encoding/json"
	"fmt"
	"strings"
)

//go:embed data/packages.json
var files embed.FS

type Runtime struct {
	Name       string `json:"name"`
	Constraint string `json:"constraint"`
	Command    string `json:"command"`
}

type Artifact struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
	Format string `json:"format"`
}

type Command struct {
	Name       string `json:"name"`
	Entrypoint string `json:"entrypoint"`
}

type Package struct {
	Name        string   `json:"name"`
	Version     string   `json:"version"`
	Description string   `json:"description"`
	Source      string   `json:"source"`
	Runtime     Runtime  `json:"runtime"`
	Artifact    Artifact `json:"artifact"`
	Command     Command  `json:"command"`
}

func All() ([]Package, error) {
	data, err := files.ReadFile("data/packages.json")
	if err != nil {
		return nil, fmt.Errorf("read embedded registry: %w", err)
	}
	var packages []Package
	if err := json.Unmarshal(data, &packages); err != nil {
		return nil, fmt.Errorf("decode embedded registry: %w", err)
	}
	return packages, nil
}

func Find(name string) (Package, error) {
	packages, err := All()
	if err != nil {
		return Package{}, err
	}
	for _, pkg := range packages {
		if strings.EqualFold(pkg.Name, name) {
			return pkg, nil
		}
	}
	return Package{}, fmt.Errorf("package %q not found", name)
}

func Search(query string) ([]Package, error) {
	packages, err := All()
	if err != nil {
		return nil, err
	}
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return packages, nil
	}
	var matches []Package
	for _, pkg := range packages {
		haystack := strings.ToLower(pkg.Name + " " + pkg.Description)
		if strings.Contains(haystack, q) {
			matches = append(matches, pkg)
		}
	}
	return matches, nil
}
