package registry

import (
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	defaultRegistryURL = "https://raw.githubusercontent.com/VyxorDev/packrail/main/internal/registry/data/packages.json"
	maxRegistryBytes   = 4 << 20
	cacheMaxAge        = time.Hour
)

// The embedded registry is only a bootstrap/offline fallback. The authoritative
// registry is the GitHub-hosted JSON file above and is cached under PACKRAIL_HOME.
//
//go:embed data/packages.json
var files embed.FS

var (
	httpClient  = &http.Client{Timeout: 30 * time.Second}
	packageName = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)
)

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
	Name       string   `json:"name"`
	Entrypoint string   `json:"entrypoint,omitempty"`
	Args       []string `json:"args,omitempty"`
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

type StatusInfo struct {
	Source       string
	URL          string
	CachePath    string
	PackageCount int
	UpdatedAt    time.Time
	Age          time.Duration
}

func All() ([]Package, error) {
	if packages, err := loadCache(); err == nil {
		return packages, nil
	}
	return loadEmbedded()
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

// RefreshIfStale updates the local registry cache when it is missing or older
// than cacheMaxAge. A caller may safely continue with All after a refresh error;
// All falls back to the last valid cache or the embedded bootstrap registry.
func RefreshIfStale() error {
	if strings.EqualFold(strings.TrimSpace(os.Getenv("PACKRAIL_REGISTRY_OFFLINE")), "1") ||
		strings.EqualFold(strings.TrimSpace(os.Getenv("PACKRAIL_REGISTRY_OFFLINE")), "true") {
		return nil
	}
	status, err := Status()
	if err != nil || status.Source == "embedded" || status.Age > cacheMaxAge {
		_, syncErr := Sync()
		return syncErr
	}
	return nil
}

// Sync downloads the authoritative registry, validates it completely, then
// atomically replaces the local cache. A malformed or partial download can
// never replace a previously valid cache.
func Sync() (StatusInfo, error) {
	registryURL := RegistryURL()
	req, err := http.NewRequest(http.MethodGet, registryURL, nil)
	if err != nil {
		return StatusInfo{}, fmt.Errorf("create registry request: %w", err)
	}
	req.Header.Set("User-Agent", "Packrail/prl")
	resp, err := httpClient.Do(req)
	if err != nil {
		return StatusInfo{}, fmt.Errorf("download registry: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return StatusInfo{}, fmt.Errorf("download registry: HTTP %s", resp.Status)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxRegistryBytes+1))
	if err != nil {
		return StatusInfo{}, fmt.Errorf("read registry: %w", err)
	}
	if len(data) > maxRegistryBytes {
		return StatusInfo{}, fmt.Errorf("registry exceeds %d bytes", maxRegistryBytes)
	}
	packages, err := decodeAndValidate(data)
	if err != nil {
		return StatusInfo{}, fmt.Errorf("validate registry: %w", err)
	}

	path, err := CachePath()
	if err != nil {
		return StatusInfo{}, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return StatusInfo{}, fmt.Errorf("create registry cache directory: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "packages-*.json.tmp")
	if err != nil {
		return StatusInfo{}, fmt.Errorf("create registry cache: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return StatusInfo{}, fmt.Errorf("write registry cache: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return StatusInfo{}, fmt.Errorf("sync registry cache: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return StatusInfo{}, fmt.Errorf("close registry cache: %w", err)
	}
	if err := replaceFile(tmpPath, path); err != nil {
		return StatusInfo{}, fmt.Errorf("commit registry cache: %w", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return StatusInfo{}, fmt.Errorf("stat registry cache: %w", err)
	}
	return StatusInfo{
		Source:       "cache",
		URL:          registryURL,
		CachePath:    path,
		PackageCount: len(packages),
		UpdatedAt:    info.ModTime(),
		Age:          time.Since(info.ModTime()),
	}, nil
}

func Status() (StatusInfo, error) {
	path, err := CachePath()
	if err != nil {
		return StatusInfo{}, err
	}
	if packages, err := loadCache(); err == nil {
		info, statErr := os.Stat(path)
		if statErr != nil {
			return StatusInfo{}, fmt.Errorf("stat registry cache: %w", statErr)
		}
		return StatusInfo{
			Source:       "cache",
			URL:          RegistryURL(),
			CachePath:    path,
			PackageCount: len(packages),
			UpdatedAt:    info.ModTime(),
			Age:          time.Since(info.ModTime()),
		}, nil
	}

	packages, err := loadEmbedded()
	if err != nil {
		return StatusInfo{}, err
	}
	return StatusInfo{
		Source:       "embedded",
		URL:          RegistryURL(),
		CachePath:    path,
		PackageCount: len(packages),
	}, nil
}

func RegistryURL() string {
	if custom := strings.TrimSpace(os.Getenv("PACKRAIL_REGISTRY_URL")); custom != "" {
		return custom
	}
	return defaultRegistryURL
}

func CachePath() (string, error) {
	home, err := packrailHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "registry", "packages.json"), nil
}

func packrailHome() (string, error) {
	if custom := strings.TrimSpace(os.Getenv("PACKRAIL_HOME")); custom != "" {
		return filepath.Abs(custom)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve user home: %w", err)
	}
	return filepath.Join(home, ".packrail"), nil
}

func loadCache() ([]Package, error) {
	path, err := CachePath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	packages, err := decodeAndValidate(data)
	if err != nil {
		return nil, fmt.Errorf("invalid registry cache: %w", err)
	}
	return packages, nil
}

func loadEmbedded() ([]Package, error) {
	data, err := files.ReadFile("data/packages.json")
	if err != nil {
		return nil, fmt.Errorf("read embedded registry: %w", err)
	}
	packages, err := decodeAndValidate(data)
	if err != nil {
		return nil, fmt.Errorf("decode embedded registry: %w", err)
	}
	return packages, nil
}

func decodeAndValidate(data []byte) ([]Package, error) {
	var packages []Package
	if err := json.Unmarshal(data, &packages); err != nil {
		return nil, fmt.Errorf("decode JSON: %w", err)
	}
	if len(packages) == 0 {
		return nil, fmt.Errorf("registry contains no packages")
	}
	seen := make(map[string]struct{}, len(packages))
	for i := range packages {
		pkg := &packages[i]
		pkg.Name = strings.ToLower(strings.TrimSpace(pkg.Name))
		if !packageName.MatchString(pkg.Name) {
			return nil, fmt.Errorf("package %d has invalid name %q", i, pkg.Name)
		}
		if _, exists := seen[pkg.Name]; exists {
			return nil, fmt.Errorf("duplicate package name %q", pkg.Name)
		}
		seen[pkg.Name] = struct{}{}
		if strings.TrimSpace(pkg.Version) == "" {
			return nil, fmt.Errorf("package %s has no version", pkg.Name)
		}
		if err := requireHTTPS(pkg.Source); err != nil {
			return nil, fmt.Errorf("package %s source: %w", pkg.Name, err)
		}
		if strings.TrimSpace(pkg.Command.Name) == "" {
			return nil, fmt.Errorf("package %s has no command name", pkg.Name)
		}
		if len(pkg.Artifacts) == 0 {
			return nil, fmt.Errorf("package %s has no artifacts", pkg.Name)
		}
		for platform, artifact := range pkg.Artifacts {
			if strings.TrimSpace(platform) == "" {
				return nil, fmt.Errorf("package %s has an empty platform key", pkg.Name)
			}
			if err := requireHTTPS(artifact.URL); err != nil {
				return nil, fmt.Errorf("package %s artifact %s URL: %w", pkg.Name, platform, err)
			}
			if !validSHA256(artifact.SHA256) {
				return nil, fmt.Errorf("package %s artifact %s has invalid SHA-256", pkg.Name, platform)
			}
			switch strings.ToLower(strings.TrimSpace(artifact.Format)) {
			case "tgz", "zip":
			default:
				return nil, fmt.Errorf("package %s artifact %s uses unsupported format %q", pkg.Name, platform, artifact.Format)
			}
			if strings.TrimSpace(artifact.Entrypoint) == "" && strings.TrimSpace(pkg.Command.Entrypoint) == "" {
				return nil, fmt.Errorf("package %s artifact %s has no entrypoint", pkg.Name, platform)
			}
		}
	}
	sort.Slice(packages, func(i, j int) bool { return packages[i].Name < packages[j].Name })
	return packages, nil
}

func requireHTTPS(value string) error {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil {
		return err
	}
	if parsed.Scheme != "https" || parsed.Host == "" {
		return fmt.Errorf("must use an absolute HTTPS URL")
	}
	return nil
}

func validSHA256(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func replaceFile(source, destination string) error {
	// Windows cannot atomically rename over an existing destination. Remove the
	// old validated cache only at commit time; the downloaded file remains in the
	// same directory so the final rename is on the same filesystem.
	if err := os.Remove(destination); err != nil && !os.IsNotExist(err) {
		return err
	}
	return os.Rename(source, destination)
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
