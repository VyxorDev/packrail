package runtimes

import (
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/VyxorDev/packrail/internal/registry"
)

// Pin is the exact runtime artifact needed on one target platform.
// Unlike a registry runtime constraint, a Pin is immutable install metadata:
// exact version, URL, checksum, archive format, and executable name(s).
type Pin struct {
	Version     string   `json:"version"`
	URL         string   `json:"url"`
	SHA256      string   `json:"sha256"`
	Format      string   `json:"format"`
	Executables []string `json:"executables"`
}

// LockedRuntime is one resolved runtime specification with platform-specific
// artifacts. A lockfile can share one LockedRuntime between several packages
// that request the same runtime constraint.
type LockedRuntime struct {
	Name       string         `json:"name"`
	Constraint string         `json:"constraint"`
	Command    string         `json:"command"`
	Artifacts  map[string]Pin `json:"artifacts"`
}

type runtimeMarker struct {
	Version string `json:"version"`
	URL     string `json:"url"`
	SHA256  string `json:"sha256"`
	Format  string `json:"format"`
}

var configuredRuntimePins = struct {
	sync.RWMutex
	items map[string]LockedRuntime
}{items: map[string]LockedRuntime{}}

// ManagedPlatforms is the platform set Packrail currently releases and tests.
func ManagedPlatforms() []string {
	return []string{
		"darwin-amd64",
		"darwin-arm64",
		"linux-amd64",
		"linux-arm64",
		"windows-amd64",
		"windows-arm64",
	}
}

// SpecKey is a deterministic identity for a registry runtime request.
func SpecKey(spec registry.Runtime) string {
	return strings.ToLower(strings.TrimSpace(spec.Name)) + "|" +
		strings.TrimSpace(spec.Constraint) + "|" +
		strings.TrimSpace(spec.Command)
}

// ConfigureLockedRuntimes replaces the process-local runtime pin set. The CLI
// is single-process and lockfile loading is explicit, so keeping this context in
// the runtime package lets the existing installer use exact pins without
// changing the public registry package model.
func ConfigureLockedRuntimes(items map[string]LockedRuntime) {
	configuredRuntimePins.Lock()
	defer configuredRuntimePins.Unlock()
	configuredRuntimePins.items = make(map[string]LockedRuntime, len(items))
	for key, item := range items {
		configuredRuntimePins.items[key] = item
	}
}

// EnsureLocked uses a configured exact runtime pin when one exists for spec;
// otherwise it falls back to Packrail's normal constraint-based resolver. This
// preserves one-off installs and v1 lockfiles while making v2 sync reproducible.
func EnsureLocked(home string, spec registry.Runtime) (Result, error) {
	if strings.TrimSpace(spec.Name) == "" {
		return Result{}, nil
	}
	configuredRuntimePins.RLock()
	locked, ok := configuredRuntimePins.items[SpecKey(spec)]
	configuredRuntimePins.RUnlock()
	if !ok {
		return Ensure(home, spec)
	}
	platform := registry.PlatformKey(runtime.GOOS, runtime.GOARCH)
	pin, ok := locked.Artifacts[platform]
	if !ok {
		return Result{}, fmt.Errorf("locked %s runtime has no artifact for %s", locked.Name, platform)
	}
	if err := validatePin(pin); err != nil {
		return Result{}, fmt.Errorf("invalid locked %s runtime for %s: %w", locked.Name, platform, err)
	}
	return installPinnedRuntime(home, locked.Name, pin)
}

// ResolveLockedRuntime converts a mutable runtime constraint into immutable
// platform artifacts suitable for packrail.lock v2.
func ResolveLockedRuntime(spec registry.Runtime, platforms []string) (LockedRuntime, error) {
	name := canonicalRuntimeName(spec.Name)
	if name == "" {
		return LockedRuntime{}, nil
	}
	if strings.TrimSpace(spec.Constraint) == "" {
		return LockedRuntime{}, fmt.Errorf("runtime %s has no constraint", name)
	}
	unique := make(map[string]struct{}, len(platforms))
	for _, platform := range platforms {
		platform = strings.ToLower(strings.TrimSpace(platform))
		if platform != "" {
			unique[platform] = struct{}{}
		}
	}
	if len(unique) == 0 {
		return LockedRuntime{}, fmt.Errorf("runtime %s has no target platforms", name)
	}
	ordered := make([]string, 0, len(unique))
	for platform := range unique {
		ordered = append(ordered, platform)
	}
	sort.Strings(ordered)

	locked := LockedRuntime{
		Name:       name,
		Constraint: strings.TrimSpace(spec.Constraint),
		Command:    strings.TrimSpace(spec.Command),
		Artifacts:  make(map[string]Pin, len(ordered)),
	}
	for _, platform := range ordered {
		goos, goarch, err := splitPlatform(platform)
		if err != nil {
			return LockedRuntime{}, err
		}
		pin, err := resolvePin(name, spec.Constraint, goos, goarch)
		if err != nil {
			return LockedRuntime{}, fmt.Errorf("resolve %s for %s: %w", name, platform, err)
		}
		if err := validatePin(pin); err != nil {
			return LockedRuntime{}, fmt.Errorf("resolved %s for %s is invalid: %w", name, platform, err)
		}
		locked.Artifacts[platform] = pin
	}
	return locked, nil
}

func ValidateLockedRuntime(locked LockedRuntime, requiredPlatforms []string) error {
	if canonicalRuntimeName(locked.Name) == "" {
		return fmt.Errorf("runtime name is required")
	}
	if strings.TrimSpace(locked.Constraint) == "" {
		return fmt.Errorf("runtime constraint is required")
	}
	if len(locked.Artifacts) == 0 {
		return fmt.Errorf("runtime %s has no pinned artifacts", locked.Name)
	}
	for platform, pin := range locked.Artifacts {
		if _, _, err := splitPlatform(platform); err != nil {
			return err
		}
		if err := validatePin(pin); err != nil {
			return fmt.Errorf("runtime %s artifact %s: %w", locked.Name, platform, err)
		}
	}
	for _, platform := range requiredPlatforms {
		if _, ok := locked.Artifacts[platform]; !ok {
			return fmt.Errorf("runtime %s is missing pin for %s", locked.Name, platform)
		}
	}
	return nil
}

func splitPlatform(platform string) (string, string, error) {
	idx := strings.LastIndex(platform, "-")
	if idx <= 0 || idx == len(platform)-1 {
		return "", "", fmt.Errorf("invalid platform %q", platform)
	}
	goos, goarch := platform[:idx], platform[idx+1:]
	switch goos {
	case "windows", "linux", "darwin":
	default:
		return "", "", fmt.Errorf("unsupported runtime OS %q", goos)
	}
	switch goarch {
	case "amd64", "arm64":
	default:
		return "", "", fmt.Errorf("unsupported runtime architecture %q", goarch)
	}
	return goos, goarch, nil
}

func resolvePin(name, constraint, goos, goarch string) (Pin, error) {
	switch name {
	case "node":
		return resolveNodePin(constraint, goos, goarch)
	case "python":
		return resolvePythonPin(constraint, goos, goarch)
	case "java":
		return resolveJavaPin(constraint, goos, goarch)
	case "dotnet":
		return resolveDotnetPin(constraint, goos, goarch)
	default:
		return Pin{}, fmt.Errorf("Packrail does not manage runtime %q", name)
	}
}

func resolveNodePin(constraint, goos, goarch string) (Pin, error) {
	minimum, _, err := minimumVersion(constraint)
	if err != nil {
		return Pin{}, fmt.Errorf("parse Node constraint %q: %w", constraint, err)
	}
	releases, err := fetchNodeIndex()
	if err != nil {
		return Pin{}, err
	}
	version, err := selectNodeVersion(releases, minimum)
	if err != nil {
		return Pin{}, err
	}
	asset, format, err := nodeAsset(version, goos, goarch)
	if err != nil {
		return Pin{}, err
	}
	checksum, err := fetchNodeChecksum(version, asset)
	if err != nil {
		return Pin{}, err
	}
	executables := []string{"node"}
	if goos == "windows" {
		executables = []string{"node.exe"}
	}
	return Pin{
		Version:     version,
		URL:         fmt.Sprintf("%s/v%s/%s", nodeDistBase, version, asset),
		SHA256:      checksum,
		Format:      format,
		Executables: executables,
	}, nil
}

func resolvePythonPin(constraint, goos, goarch string) (Pin, error) {
	minMajor, minMinor, err := minimumVersion(constraint)
	if err != nil {
		return Pin{}, fmt.Errorf("parse Python constraint %q: %w", constraint, err)
	}
	triple, err := pythonTarget(goos, goarch)
	if err != nil {
		return Pin{}, err
	}
	var latest pythonLatest
	if err := getJSON(pythonLatestURL, &latest); err != nil {
		return Pin{}, fmt.Errorf("fetch Python release metadata: %w", err)
	}
	if strings.TrimSpace(latest.Tag) == "" {
		return Pin{}, fmt.Errorf("Python release metadata did not contain a tag")
	}
	var release githubRelease
	if err := getJSON(pythonReleaseAPI+url.PathEscape(latest.Tag), &release); err != nil {
		return Pin{}, fmt.Errorf("fetch Python release assets: %w", err)
	}
	type candidate struct {
		asset githubAsset
		ver   string
		major int
		minor int
		patch int
	}
	var candidates []candidate
	suffix := "-" + triple + "-install_only_stripped.tar.gz"
	for _, asset := range release.Assets {
		if !strings.HasPrefix(asset.Name, "cpython-") || !strings.HasSuffix(asset.Name, suffix) || strings.Contains(asset.Name, "freethreaded") {
			continue
		}
		versionText := strings.TrimSuffix(strings.TrimPrefix(asset.Name, "cpython-"), suffix)
		if plus := strings.IndexByte(versionText, '+'); plus >= 0 {
			versionText = versionText[:plus]
		}
		major, minor, patch, parseErr := parseVersion(versionText)
		if parseErr != nil || !atLeast(major, minor, minMajor, minMinor) {
			continue
		}
		digest := strings.TrimPrefix(strings.TrimSpace(asset.Digest), "sha256:")
		if len(digest) != 64 {
			continue
		}
		candidates = append(candidates, candidate{asset: asset, ver: versionText, major: major, minor: minor, patch: patch})
	}
	if len(candidates) == 0 {
		return Pin{}, fmt.Errorf("no portable Python release satisfies %s on %s/%s", constraint, goos, goarch)
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].major != candidates[j].major {
			return candidates[i].major > candidates[j].major
		}
		if candidates[i].minor != candidates[j].minor {
			return candidates[i].minor > candidates[j].minor
		}
		return candidates[i].patch > candidates[j].patch
	})
	chosen := candidates[0]
	executables := []string{"python3", "python"}
	if goos == "windows" {
		executables = []string{"python.exe"}
	}
	return Pin{Version: chosen.ver, URL: chosen.asset.BrowserDownloadURL, SHA256: strings.TrimPrefix(chosen.asset.Digest, "sha256:"), Format: "tgz", Executables: executables}, nil
}

func resolveJavaPin(constraint, goos, goarch string) (Pin, error) {
	major, _, err := minimumVersion(constraint)
	if err != nil {
		return Pin{}, fmt.Errorf("parse Java constraint %q: %w", constraint, err)
	}
	osName, arch, err := adoptiumPlatform(goos, goarch)
	if err != nil {
		return Pin{}, err
	}
	endpoint := fmt.Sprintf("%s/assets/latest/%d/hotspot?architecture=%s&image_type=jre&os=%s&vendor=eclipse", adoptiumAPIBase, major, arch, osName)
	var assets []adoptiumAsset
	if err := getJSON(endpoint, &assets); err != nil {
		return Pin{}, fmt.Errorf("fetch Java release metadata: %w", err)
	}
	if len(assets) == 0 {
		return Pin{}, fmt.Errorf("no Eclipse Temurin Java %d JRE is available for %s/%s", major, goos, goarch)
	}
	asset := assets[0]
	pkg := asset.Binary.Package
	checksum := strings.TrimSpace(pkg.Checksum)
	if pkg.ChecksumLink != "" {
		if text, fetchErr := getText(pkg.ChecksumLink, 1<<20); fetchErr == nil {
			if fields := strings.Fields(text); len(fields) > 0 && len(fields[0]) == 64 {
				checksum = fields[0]
			}
		}
	}
	format := archiveFormat(pkg.Name)
	if len(checksum) != 64 || format == "" {
		return Pin{}, fmt.Errorf("Java release metadata is incomplete")
	}
	version := strings.TrimSpace(asset.Version.Semver)
	if version == "" {
		version = strings.TrimPrefix(strings.TrimSpace(asset.ReleaseName), "jdk-")
	}
	if version == "" {
		version = strconv.Itoa(major)
	}
	executables := []string{"java"}
	if goos == "windows" {
		executables = []string{"java.exe"}
	}
	return Pin{Version: sanitizeVersion(version), URL: pkg.Link, SHA256: checksum, Format: format, Executables: executables}, nil
}

func resolveDotnetPin(constraint, goos, goarch string) (Pin, error) {
	minimum, _, err := minimumVersion(constraint)
	if err != nil {
		return Pin{}, fmt.Errorf("parse .NET constraint %q: %w", constraint, err)
	}
	var index dotnetIndex
	if err := getJSON(dotnetReleaseIndex, &index); err != nil {
		return Pin{}, fmt.Errorf("fetch .NET release index: %w", err)
	}
	type channel struct {
		major  int
		lts    bool
		json   string
		latest string
	}
	var channels []channel
	for _, item := range index.Channels {
		majorText := item.ChannelVersion
		if dot := strings.IndexByte(majorText, '.'); dot >= 0 {
			majorText = majorText[:dot]
		}
		major, parseErr := strconv.Atoi(majorText)
		if parseErr != nil || major < minimum || strings.TrimSpace(item.ReleasesJSON) == "" {
			continue
		}
		channels = append(channels, channel{major: major, lts: strings.EqualFold(item.ReleaseType, "lts"), json: item.ReleasesJSON, latest: item.LatestRelease})
	}
	if len(channels) == 0 {
		return Pin{}, fmt.Errorf("no supported .NET release channel satisfies %s", constraint)
	}
	hasLTS := false
	for _, item := range channels {
		if item.lts {
			hasLTS = true
			break
		}
	}
	sort.Slice(channels, func(i, j int) bool {
		if hasLTS && channels[i].lts != channels[j].lts {
			return channels[i].lts
		}
		return channels[i].major > channels[j].major
	})
	selected := channels[0]
	var releases dotnetChannel
	if err := getJSON(selected.json, &releases); err != nil {
		return Pin{}, fmt.Errorf("fetch .NET %d release metadata: %w", selected.major, err)
	}
	rid, err := dotnetRID(goos, goarch)
	if err != nil {
		return Pin{}, err
	}
	var releaseVersion, runtimeVersion string
	var files []dotnetFile
	for _, release := range releases.Releases {
		if release.ReleaseVersion == selected.latest {
			releaseVersion, runtimeVersion, files = release.ReleaseVersion, release.Runtime.Version, release.Runtime.Files
			break
		}
	}
	if len(files) == 0 && len(releases.Releases) > 0 {
		releaseVersion, runtimeVersion, files = releases.Releases[0].ReleaseVersion, releases.Releases[0].Runtime.Version, releases.Releases[0].Runtime.Files
	}
	for _, file := range files {
		if file.RID != rid || !strings.HasPrefix(file.Name, "dotnet-runtime-") || strings.TrimSpace(file.URL) == "" || strings.TrimSpace(file.Hash) == "" {
			continue
		}
		format := archiveFormat(file.Name)
		if format == "" {
			continue
		}
		version := runtimeVersion
		if version == "" {
			version = releaseVersion
		}
		executables := []string{"dotnet"}
		if goos == "windows" {
			executables = []string{"dotnet.exe"}
		}
		return Pin{Version: sanitizeVersion(version), URL: file.URL, SHA256: file.Hash, Format: format, Executables: executables}, nil
	}
	return Pin{}, fmt.Errorf(".NET %s has no runtime archive for %s", releaseVersion, rid)
}

func validatePin(pin Pin) error {
	version := strings.TrimSpace(pin.Version)
	if version == "" || strings.ContainsAny(version, "/\\\x00") || version == "." || version == ".." {
		return fmt.Errorf("unsafe runtime version %q", pin.Version)
	}
	parsed, err := url.Parse(strings.TrimSpace(pin.URL))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return fmt.Errorf("runtime URL must be absolute HTTPS")
	}
	checksum := strings.TrimSpace(pin.SHA256)
	if len(checksum) != 64 && len(checksum) != 128 {
		return fmt.Errorf("runtime checksum must be SHA-256 or SHA-512")
	}
	if _, err := hex.DecodeString(checksum); err != nil {
		return fmt.Errorf("runtime checksum is not hexadecimal")
	}
	if pin.Format != "zip" && pin.Format != "tgz" {
		return fmt.Errorf("unsupported runtime format %q", pin.Format)
	}
	if len(pin.Executables) == 0 {
		return fmt.Errorf("runtime executable list is empty")
	}
	for _, executable := range pin.Executables {
		if executable == "" || filepath.Base(executable) != executable || strings.ContainsAny(executable, "/\\\x00") {
			return fmt.Errorf("unsafe runtime executable %q", executable)
		}
	}
	return nil
}

func installPinnedRuntime(home, name string, pin Pin) (Result, error) {
	name = canonicalRuntimeName(name)
	platform := registry.PlatformKey(runtime.GOOS, runtime.GOARCH)
	targetDir := filepath.Join(home, "runtimes", name, pin.Version, platform)
	markerPath := filepath.Join(targetDir, ".packrail-runtime.json")
	if data, err := os.ReadFile(markerPath); err == nil {
		var marker runtimeMarker
		if json.Unmarshal(data, &marker) == nil && marker.Version == pin.Version && marker.URL == pin.URL && strings.EqualFold(marker.SHA256, pin.SHA256) && marker.Format == pin.Format {
			if executable, findErr := findRuntimeExecutable(targetDir, pin.Executables); findErr == nil {
				return Result{Executable: executable, Version: pin.Version, Managed: true}, nil
			}
		}
	}

	staging, err := runtimeStaging(home)
	if err != nil {
		return Result{}, err
	}
	suffix := ".tgz"
	if pin.Format == "zip" {
		suffix = ".zip"
	}
	tmp, err := os.CreateTemp(staging, name+"-locked-*"+suffix)
	if err != nil {
		return Result{}, fmt.Errorf("create pinned runtime download: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := downloadPinned(pin.URL, pin.SHA256, tmp); err != nil {
		tmp.Close()
		return Result{}, err
	}
	if err := tmp.Close(); err != nil {
		return Result{}, fmt.Errorf("close pinned runtime download: %w", err)
	}

	stageDir, err := os.MkdirTemp(staging, name+"-locked-extract-*")
	if err != nil {
		return Result{}, fmt.Errorf("create pinned runtime extraction directory: %w", err)
	}
	defer os.RemoveAll(stageDir)
	if err := extractRuntimeArchive(tmpPath, stageDir, pin.Format); err != nil {
		return Result{}, err
	}
	stageExecutable, err := findRuntimeExecutable(stageDir, pin.Executables)
	if err != nil {
		return Result{}, fmt.Errorf("locate pinned %s runtime executable: %w", name, err)
	}
	relativeExecutable, err := filepath.Rel(stageDir, stageExecutable)
	if err != nil {
		return Result{}, err
	}
	marker := runtimeMarker{Version: pin.Version, URL: pin.URL, SHA256: pin.SHA256, Format: pin.Format}
	markerData, err := json.MarshalIndent(marker, "", "  ")
	if err != nil {
		return Result{}, err
	}
	if err := os.WriteFile(filepath.Join(stageDir, ".packrail-runtime.json"), append(markerData, '\n'), 0o644); err != nil {
		return Result{}, fmt.Errorf("write pinned runtime marker: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(targetDir), 0o755); err != nil {
		return Result{}, fmt.Errorf("create pinned runtime directory: %w", err)
	}
	backupDir := targetDir + ".packrail-backup"
	_ = os.RemoveAll(backupDir)
	hadExisting := false
	if _, err := os.Stat(targetDir); err == nil {
		if err := os.Rename(targetDir, backupDir); err != nil {
			return Result{}, fmt.Errorf("stage existing runtime for replacement: %w", err)
		}
		hadExisting = true
	}
	if err := os.Rename(stageDir, targetDir); err != nil {
		if hadExisting {
			_ = os.Rename(backupDir, targetDir)
		}
		return Result{}, fmt.Errorf("commit pinned runtime installation: %w", err)
	}
	if hadExisting {
		_ = os.RemoveAll(backupDir)
	}
	targetExecutable := filepath.Join(targetDir, relativeExecutable)
	if runtime.GOOS != "windows" {
		_ = os.Chmod(targetExecutable, 0o755)
	}
	return Result{Executable: targetExecutable, Version: pin.Version, Managed: true}, nil
}

func downloadPinned(downloadURL, expected string, dst *os.File) error {
	req, err := http.NewRequest(http.MethodGet, downloadURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "Packrail/prl")
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("download pinned runtime: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("download pinned runtime: HTTP %s", resp.Status)
	}
	var digest hash.Hash
	expected = strings.TrimSpace(expected)
	switch len(expected) {
	case 64:
		digest = sha256.New()
	case 128:
		digest = sha512.New()
	default:
		return fmt.Errorf("unsupported pinned runtime checksum length %d", len(expected))
	}
	if _, err := io.Copy(io.MultiWriter(dst, digest), resp.Body); err != nil {
		return fmt.Errorf("download pinned runtime: %w", err)
	}
	actual := hex.EncodeToString(digest.Sum(nil))
	if !strings.EqualFold(actual, expected) {
		return fmt.Errorf("pinned runtime checksum mismatch: expected %s, got %s", expected, actual)
	}
	return nil
}
