package registry

import (
	"embed"
	"encoding/json"
	"fmt"
	"sort"
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
	URL        string `json:"url"`
	SHA256     string `json:"sha256"`
	Format     string `json:"format"`
	Entrypoint string `json:"entrypoint,omitempty"`
}

type Command struct {
	Name       string `json:"name"`
	Entrypoint string `json:"entrypoint,omitempty"`
}

type Package struct {
	Name        string              `json:"name"`
	Version     string              `json:"version"`
	Description string              `json:"description"`
	Source      string              `json:"source"`
	Runtime     Runtime             `json:"runtime,omitempty"`
	Artifacts   map[string]Artifact `json:"artifacts"`
	Command     Command             `json:"command"`
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

func PlatformKey(goos, goarch string) string {
	return strings.ToLower(strings.TrimSpace(goos)) + "-" + strings.ToLower(strings.TrimSpace(goarch))
}

func ArtifactFor(pkg Package, goos, goarch string) (Artifact, error) {
	key := PlatformKey(goos, goarch)
	artifact, ok := pkg.Artifacts[key]
	if !ok {
		artifact, ok = pkg.Artifacts["any"]
	}
	if !ok {
		return Artifact{}, fmt.Errorf("package %s %s does not support %s", pkg.Name, pkg.Version, key)
	}
	if strings.TrimSpace(artifact.URL) == "" || strings.TrimSpace(artifact.SHA256) == "" || strings.TrimSpace(artifact.Format) == "" {
		return Artifact{}, fmt.Errorf("package %s has an incomplete artifact definition for %s", pkg.Name, key)
	}
	return artifact, nil
}

func EntrypointFor(pkg Package, artifact Artifact) (string, error) {
	entrypoint := strings.TrimSpace(artifact.Entrypoint)
	if entrypoint == "" {
		entrypoint = strings.TrimSpace(pkg.Command.Entrypoint)
	}
	if entrypoint == "" {
		return "", fmt.Errorf("package %s does not define an entrypoint", pkg.Name)
	}
	return entrypoint, nil
}

func Platforms(pkg Package) []string {
	platforms := make([]string, 0, len(pkg.Artifacts))
	for platform := range pkg.Artifacts {
		platforms = append(platforms, platform)
	}
	sort.Strings(platforms)
	return platforms
}
