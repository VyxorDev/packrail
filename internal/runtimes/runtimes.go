package runtimes

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/VyxorDev/packrail/internal/registry"
)

const (
	nodeDistBase        = "https://nodejs.org/dist"
	pythonLatestURL     = "https://raw.githubusercontent.com/astral-sh/python-build-standalone/latest-release/latest-release.json"
	pythonReleaseAPI    = "https://api.github.com/repos/astral-sh/python-build-standalone/releases/tags/"
	adoptiumAPIBase     = "https://api.adoptium.net/v3"
	dotnetReleaseIndex  = "https://dotnetcli.blob.core.windows.net/dotnet/release-metadata/releases-index.json"
	maxRuntimeMetaBytes = 8 << 20
)

type Result struct {
	Executable string
	Version    string
	Managed    bool
}

type nodeRelease struct {
	Version string `json:"version"`
	LTS     any    `json:"lts"`
}

type pythonLatest struct {
	Tag string `json:"tag"`
}

type githubRelease struct {
	Assets []githubAsset `json:"assets"`
}

type githubAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Digest             string `json:"digest"`
}

type adoptiumAsset struct {
	ReleaseName string `json:"release_name"`
	Binary      struct {
		Package struct {
			Checksum     string `json:"checksum"`
			ChecksumLink string `json:"checksum_link"`
			Link         string `json:"link"`
			Name         string `json:"name"`
		} `json:"package"`
	} `json:"binary"`
	Version struct {
		Semver string `json:"semver"`
	} `json:"version"`
}

type dotnetIndex struct {
	Channels []struct {
		ChannelVersion string `json:"channel-version"`
		LatestRelease  string `json:"latest-release"`
		ReleaseType    string `json:"release-type"`
		ReleasesJSON   string `json:"releases.json"`
	} `json:"releases-index"`
}

type dotnetChannel struct {
	Releases []struct {
		ReleaseVersion string `json:"release-version"`
		Runtime        struct {
			Version string       `json:"version"`
			Files   []dotnetFile `json:"files"`
		} `json:"runtime"`
	} `json:"releases"`
}

type dotnetFile struct {
	Name string `json:"name"`
	RID  string `json:"rid"`
	URL  string `json:"url"`
	Hash string `json:"hash"`
}

var httpClient = &http.Client{Timeout: 10 * time.Minute}

func Ensure(home string, spec registry.Runtime) (Result, error) {
	name := strings.ToLower(strings.TrimSpace(spec.Name))
	if name == "" {
		return Result{}, nil
	}

	switch name {
	case "node", "nodejs":
		return ensureNode(home, spec.Constraint)
	case "python", "python3":
		return ensurePython(home, spec.Constraint)
	case "java", "jre":
		return ensureJava(home, spec.Constraint)
	case "dotnet", ".net":
		return ensureDotnet(home, spec.Constraint)
	default:
		return Result{}, fmt.Errorf("Packrail does not manage runtime %q", spec.Name)
	}
}

func InstalledVersions(home, name string) ([]string, error) {
	root := filepath.Join(home, "runtimes", canonicalRuntimeName(name))
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read managed runtimes: %w", err)
	}
	versions := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			versions = append(versions, entry.Name())
		}
	}
	sort.Strings(versions)
	return versions, nil
}

func canonicalRuntimeName(name string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "nodejs":
		return "node"
	case "python3":
		return "python"
	case "jre":
		return "java"
	case ".net":
		return "dotnet"
	default:
		return strings.ToLower(strings.TrimSpace(name))
	}
}

func ensureNode(home, constraint string) (Result, error) {
	minimum, _, err := minimumVersion(constraint)
	if err != nil {
		return Result{}, fmt.Errorf("parse Node constraint %q: %w", constraint, err)
	}

	releases, err := fetchNodeIndex()
	if err != nil {
		return Result{}, err
	}
	version, err := selectNodeVersion(releases, minimum)
	if err != nil {
		return Result{}, err
	}

	asset, archiveKind, err := nodeAsset(version, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return Result{}, err
	}
	executableName := "node"
	if runtime.GOOS == "windows" {
		executableName = "node.exe"
	}
	targetDir := filepath.Join(home, "runtimes", "node", version, runtime.GOOS+"-"+runtime.GOARCH)
	target := filepath.Join(targetDir, executableName)
	if info, err := os.Stat(target); err == nil && !info.IsDir() {
		return Result{Executable: target, Version: version, Managed: true}, nil
	}

	checksum, err := fetchNodeChecksum(version, asset)
	if err != nil {
		return Result{}, err
	}
	staging, err := runtimeStaging(home)
	if err != nil {
		return Result{}, err
	}
	tmp, err := os.CreateTemp(staging, "node-*")
	if err != nil {
		return Result{}, fmt.Errorf("create runtime download: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	downloadURL := fmt.Sprintf("%s/v%s/%s", nodeDistBase, version, asset)
	if err := downloadAndVerify(downloadURL, checksum, tmp); err != nil {
		tmp.Close()
		return Result{}, err
	}
	if err := tmp.Close(); err != nil {
		return Result{}, fmt.Errorf("close runtime download: %w", err)
	}

	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return Result{}, fmt.Errorf("create runtime directory: %w", err)
	}
	stagedTarget := target + ".tmp"
	_ = os.Remove(stagedTarget)
	if err := extractNodeExecutable(tmpPath, stagedTarget, archiveKind); err != nil {
		return Result{}, err
	}
	_ = os.Remove(target)
	if err := os.Rename(stagedTarget, target); err != nil {
		return Result{}, fmt.Errorf("commit Node runtime: %w", err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(target, 0o755); err != nil {
			return Result{}, fmt.Errorf("mark Node executable: %w", err)
		}
	}
	return Result{Executable: target, Version: version, Managed: true}, nil
}

func ensurePython(home, constraint string) (Result, error) {
	minMajor, minMinor, err := minimumVersion(constraint)
	if err != nil {
		return Result{}, fmt.Errorf("parse Python constraint %q: %w", constraint, err)
	}
	triple, err := pythonTarget(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return Result{}, err
	}

	var latest pythonLatest
	if err := getJSON(pythonLatestURL, &latest); err != nil {
		return Result{}, fmt.Errorf("fetch Python release metadata: %w", err)
	}
	if strings.TrimSpace(latest.Tag) == "" {
		return Result{}, fmt.Errorf("Python release metadata did not contain a tag")
	}
	var release githubRelease
	if err := getJSON(pythonReleaseAPI+url.PathEscape(latest.Tag), &release); err != nil {
		return Result{}, fmt.Errorf("fetch Python release assets: %w", err)
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
		beforeTarget := strings.TrimSuffix(strings.TrimPrefix(asset.Name, "cpython-"), suffix)
		versionText := beforeTarget
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
		return Result{}, fmt.Errorf("no portable Python release satisfies %s on %s/%s", constraint, runtime.GOOS, runtime.GOARCH)
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
	names := []string{"python3", "python"}
	if runtime.GOOS == "windows" {
		names = []string{"python.exe"}
	}
	return installArchiveRuntime(home, "python", chosen.ver, chosen.asset.BrowserDownloadURL, strings.TrimPrefix(chosen.asset.Digest, "sha256:"), "tgz", names)
}

func ensureJava(home, constraint string) (Result, error) {
	major, _, err := minimumVersion(constraint)
	if err != nil {
		return Result{}, fmt.Errorf("parse Java constraint %q: %w", constraint, err)
	}
	osName, arch, err := adoptiumPlatform(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return Result{}, err
	}
	endpoint := fmt.Sprintf("%s/assets/latest/%d/hotspot?architecture=%s&image_type=jre&os=%s&vendor=eclipse", adoptiumAPIBase, major, arch, osName)
	var assets []adoptiumAsset
	if err := getJSON(endpoint, &assets); err != nil {
		return Result{}, fmt.Errorf("fetch Java release metadata: %w", err)
	}
	if len(assets) == 0 {
		return Result{}, fmt.Errorf("no Eclipse Temurin Java %d JRE is available for %s/%s", major, runtime.GOOS, runtime.GOARCH)
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
	if len(checksum) != 64 {
		return Result{}, fmt.Errorf("Java release did not publish a valid SHA-256 checksum")
	}
	format := archiveFormat(pkg.Name)
	if format == "" {
		return Result{}, fmt.Errorf("unsupported Java runtime archive %q", pkg.Name)
	}
	version := strings.TrimSpace(asset.Version.Semver)
	if version == "" {
		version = strings.TrimPrefix(strings.TrimSpace(asset.ReleaseName), "jdk-")
	}
	if version == "" {
		version = strconv.Itoa(major)
	}
	exe := []string{"java"}
	if runtime.GOOS == "windows" {
		exe = []string{"java.exe"}
	}
	return installArchiveRuntime(home, "java", sanitizeVersion(version), pkg.Link, checksum, format, exe)
}

func ensureDotnet(home, constraint string) (Result, error) {
	minimum, _, err := minimumVersion(constraint)
	if err != nil {
		return Result{}, fmt.Errorf("parse .NET constraint %q: %w", constraint, err)
	}
	var index dotnetIndex
	if err := getJSON(dotnetReleaseIndex, &index); err != nil {
		return Result{}, fmt.Errorf("fetch .NET release index: %w", err)
	}

	type channel struct {
		major int
		lts   bool
		json  string
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
		return Result{}, fmt.Errorf("no supported .NET release channel satisfies %s", constraint)
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
		return Result{}, fmt.Errorf("fetch .NET %d release metadata: %w", selected.major, err)
	}
	rid, err := dotnetRID(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return Result{}, err
	}
	var releaseVersion string
	var runtimeVersion string
	var files []dotnetFile
	for _, release := range releases.Releases {
		if release.ReleaseVersion == selected.latest {
			releaseVersion = release.ReleaseVersion
			runtimeVersion = release.Runtime.Version
			files = release.Runtime.Files
			break
		}
	}
	if len(files) == 0 && len(releases.Releases) > 0 {
		releaseVersion = releases.Releases[0].ReleaseVersion
		runtimeVersion = releases.Releases[0].Runtime.Version
		files = releases.Releases[0].Runtime.Files
	}
	for _, file := range files {
		if file.RID != rid || strings.TrimSpace(file.URL) == "" || strings.TrimSpace(file.Hash) == "" {
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
		exe := []string{"dotnet"}
		if runtime.GOOS == "windows" {
			exe = []string{"dotnet.exe"}
		}
		return installArchiveRuntime(home, "dotnet", sanitizeVersion(version), file.URL, file.Hash, format, exe)
	}
	return Result{}, fmt.Errorf(".NET %s has no runtime archive for %s", releaseVersion, rid)
}

func fetchNodeIndex() ([]nodeRelease, error) {
	var releases []nodeRelease
	if err := getJSON(nodeDistBase+"/index.json", &releases); err != nil {
		return nil, fmt.Errorf("fetch Node release index: %w", err)
	}
	return releases, nil
}

func getJSON(endpoint string, dst any) error {
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "Packrail/prl")
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %s", resp.Status)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, maxRuntimeMetaBytes)).Decode(dst)
}

func getText(endpoint string, limit int64) (string, error) {
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Packrail/prl")
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("HTTP %s", resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit))
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func selectNodeVersion(releases []nodeRelease, minimum int) (string, error) {
	type candidate struct {
		version string
		major   int
		minor   int
		patch   int
		lts     bool
	}
	var candidates []candidate
	for _, release := range releases {
		major, minor, patch, err := parseVersion(release.Version)
		if err != nil || major < minimum {
			continue
		}
		lts := false
		switch value := release.LTS.(type) {
		case string:
			lts = strings.TrimSpace(value) != ""
		case bool:
			lts = value
		}
		candidates = append(candidates, candidate{release.Version, major, minor, patch, lts})
	}
	if len(candidates) == 0 {
		return "", fmt.Errorf("no Node release satisfies >=%d", minimum)
	}
	hasLTS := false
	for _, c := range candidates {
		if c.lts {
			hasLTS = true
			break
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		if hasLTS && candidates[i].lts != candidates[j].lts {
			return candidates[i].lts
		}
		if candidates[i].major != candidates[j].major {
			return candidates[i].major > candidates[j].major
		}
		if candidates[i].minor != candidates[j].minor {
			return candidates[i].minor > candidates[j].minor
		}
		return candidates[i].patch > candidates[j].patch
	})
	return strings.TrimPrefix(candidates[0].version, "v"), nil
}

func minimumVersion(constraint string) (int, int, error) {
	value := strings.TrimSpace(constraint)
	if value == "" {
		return 0, 0, fmt.Errorf("runtime constraint is required")
	}
	value = strings.TrimPrefix(value, ">=")
	value = strings.TrimPrefix(value, "v")
	parts := strings.Split(value, ".")
	major, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil || major <= 0 {
		return 0, 0, fmt.Errorf("unsupported constraint")
	}
	minor := 0
	if len(parts) > 1 {
		minorText := parts[1]
		if i := strings.IndexAny(minorText, " <>=~^*"); i >= 0 {
			minorText = minorText[:i]
		}
		if strings.TrimSpace(minorText) != "" {
			minor, err = strconv.Atoi(strings.TrimSpace(minorText))
			if err != nil || minor < 0 {
				return 0, 0, fmt.Errorf("unsupported constraint")
			}
		}
	}
	return major, minor, nil
}

func parseVersion(value string) (int, int, int, error) {
	parts := strings.Split(strings.TrimPrefix(value, "v"), ".")
	if len(parts) < 3 {
		return 0, 0, 0, fmt.Errorf("invalid version")
	}
	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, 0, err
	}
	minor, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, 0, err
	}
	patchText := parts[2]
	if i := strings.IndexAny(patchText, "-+"); i >= 0 {
		patchText = patchText[:i]
	}
	patch, err := strconv.Atoi(patchText)
	if err != nil {
		return 0, 0, 0, err
	}
	return major, minor, patch, nil
}

func atLeast(major, minor, minMajor, minMinor int) bool {
	return major > minMajor || (major == minMajor && minor >= minMinor)
}

func nodeAsset(version, goos, goarch string) (string, string, error) {
	arch := ""
	switch goarch {
	case "amd64":
		arch = "x64"
	case "arm64":
		arch = "arm64"
	default:
		return "", "", fmt.Errorf("Node runtime is not supported on architecture %s", goarch)
	}
	prefix := "node-v" + strings.TrimPrefix(version, "v") + "-"
	switch goos {
	case "windows":
		return prefix + "win-" + arch + ".zip", "zip", nil
	case "linux":
		return prefix + "linux-" + arch + ".tar.gz", "tgz", nil
	case "darwin":
		return prefix + "darwin-" + arch + ".tar.gz", "tgz", nil
	default:
		return "", "", fmt.Errorf("Node runtime is not supported on OS %s", goos)
	}
}

func pythonTarget(goos, goarch string) (string, error) {
	arch := map[string]string{"amd64": "x86_64", "arm64": "aarch64"}[goarch]
	if arch == "" {
		return "", fmt.Errorf("Python runtime is not supported on architecture %s", goarch)
	}
	switch goos {
	case "windows":
		return arch + "-pc-windows-msvc", nil
	case "linux":
		return arch + "-unknown-linux-gnu", nil
	case "darwin":
		return arch + "-apple-darwin", nil
	default:
		return "", fmt.Errorf("Python runtime is not supported on OS %s", goos)
	}
}

func adoptiumPlatform(goos, goarch string) (string, string, error) {
	arch := map[string]string{"amd64": "x64", "arm64": "aarch64"}[goarch]
	if arch == "" {
		return "", "", fmt.Errorf("Java runtime is not supported on architecture %s", goarch)
	}
	osName := map[string]string{"windows": "windows", "linux": "linux", "darwin": "mac"}[goos]
	if osName == "" {
		return "", "", fmt.Errorf("Java runtime is not supported on OS %s", goos)
	}
	return osName, arch, nil
}

func dotnetRID(goos, goarch string) (string, error) {
	arch := map[string]string{"amd64": "x64", "arm64": "arm64"}[goarch]
	if arch == "" {
		return "", fmt.Errorf(".NET runtime is not supported on architecture %s", goarch)
	}
	prefix := map[string]string{"windows": "win", "linux": "linux", "darwin": "osx"}[goos]
	if prefix == "" {
		return "", fmt.Errorf(".NET runtime is not supported on OS %s", goos)
	}
	return prefix + "-" + arch, nil
}

func fetchNodeChecksum(version, asset string) (string, error) {
	text, err := getText(fmt.Sprintf("%s/v%s/SHASUMS256.txt", nodeDistBase, version), 1<<20)
	if err != nil {
		return "", fmt.Errorf("fetch Node checksums: %w", err)
	}
	for _, line := range strings.Split(text, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[1] == asset {
			return fields[0], nil
		}
	}
	return "", fmt.Errorf("checksum for Node asset %s was not published", asset)
}

func runtimeStaging(home string) (string, error) {
	staging := filepath.Join(home, "staging")
	if err := os.MkdirAll(staging, 0o755); err != nil {
		return "", fmt.Errorf("create runtime staging directory: %w", err)
	}
	return staging, nil
}

func installArchiveRuntime(home, name, version, downloadURL, checksum, format string, executableNames []string) (Result, error) {
	platform := runtime.GOOS + "-" + runtime.GOARCH
	targetDir := filepath.Join(home, "runtimes", name, version, platform)
	if executable, err := findRuntimeExecutable(targetDir, executableNames); err == nil {
		return Result{Executable: executable, Version: version, Managed: true}, nil
	}

	staging, err := runtimeStaging(home)
	if err != nil {
		return Result{}, err
	}
	suffix := ".tgz"
	if format == "zip" {
		suffix = ".zip"
	}
	tmp, err := os.CreateTemp(staging, name+"-*"+suffix)
	if err != nil {
		return Result{}, fmt.Errorf("create runtime download: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := downloadAndVerify(downloadURL, checksum, tmp); err != nil {
		tmp.Close()
		return Result{}, err
	}
	if err := tmp.Close(); err != nil {
		return Result{}, fmt.Errorf("close runtime download: %w", err)
	}

	stageDir, err := os.MkdirTemp(staging, name+"-extract-*")
	if err != nil {
		return Result{}, fmt.Errorf("create runtime extraction directory: %w", err)
	}
	defer os.RemoveAll(stageDir)
	if err := extractRuntimeArchive(tmpPath, stageDir, format); err != nil {
		return Result{}, err
	}
	stageExecutable, err := findRuntimeExecutable(stageDir, executableNames)
	if err != nil {
		return Result{}, fmt.Errorf("locate %s runtime executable: %w", name, err)
	}
	relativeExecutable, err := filepath.Rel(stageDir, stageExecutable)
	if err != nil {
		return Result{}, err
	}
	if err := os.MkdirAll(filepath.Dir(targetDir), 0o755); err != nil {
		return Result{}, fmt.Errorf("create runtime version directory: %w", err)
	}
	if err := os.RemoveAll(targetDir); err != nil {
		return Result{}, fmt.Errorf("replace runtime directory: %w", err)
	}
	if err := os.Rename(stageDir, targetDir); err != nil {
		return Result{}, fmt.Errorf("commit runtime installation: %w", err)
	}
	targetExecutable := filepath.Join(targetDir, relativeExecutable)
	if runtime.GOOS != "windows" {
		_ = os.Chmod(targetExecutable, 0o755)
	}
	return Result{Executable: targetExecutable, Version: version, Managed: true}, nil
}

func downloadAndVerify(downloadURL, expected string, dst *os.File) error {
	req, err := http.NewRequest(http.MethodGet, downloadURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "Packrail/prl")
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("download runtime: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("download runtime: HTTP %s", resp.Status)
	}

	var digest hash.Hash
	expected = strings.TrimSpace(expected)
	switch len(expected) {
	case 64:
		digest = sha256.New()
	case 128:
		digest = sha512.New()
	default:
		return fmt.Errorf("unsupported runtime checksum length %d", len(expected))
	}
	if _, err := io.Copy(io.MultiWriter(dst, digest), resp.Body); err != nil {
		return fmt.Errorf("download runtime: %w", err)
	}
	actual := hex.EncodeToString(digest.Sum(nil))
	if !strings.EqualFold(actual, expected) {
		return fmt.Errorf("runtime checksum mismatch: expected %s, got %s", expected, actual)
	}
	return nil
}

func archiveFormat(name string) string {
	lower := strings.ToLower(strings.TrimSpace(name))
	switch {
	case strings.HasSuffix(lower, ".zip"):
		return "zip"
	case strings.HasSuffix(lower, ".tar.gz"), strings.HasSuffix(lower, ".tgz"):
		return "tgz"
	default:
		return ""
	}
}

func extractRuntimeArchive(archivePath, destination, format string) error {
	switch format {
	case "zip":
		return extractRuntimeZIP(archivePath, destination)
	case "tgz":
		return extractRuntimeTGZ(archivePath, destination)
	default:
		return fmt.Errorf("unsupported runtime archive format %q", format)
	}
}

func extractRuntimeZIP(archivePath, destination string) error {
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return fmt.Errorf("open runtime zip: %w", err)
	}
	defer reader.Close()
	root, err := filepath.Abs(destination)
	if err != nil {
		return err
	}
	for _, file := range reader.File {
		target, err := safeRuntimeTarget(root, file.Name)
		if err != nil {
			return err
		}
		info := file.FileInfo()
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("runtime zip contains unsupported symlink %q", file.Name)
		}
		if info.IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		src, err := file.Open()
		if err != nil {
			return err
		}
		mode := info.Mode().Perm()
		if mode == 0 {
			mode = 0o644
		}
		copyErr := copyRuntimeFile(src, target, mode)
		closeErr := src.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}

func extractRuntimeTGZ(archivePath, destination string) error {
	file, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("open runtime archive: %w", err)
	}
	defer file.Close()
	gz, err := gzip.NewReader(file)
	if err != nil {
		return fmt.Errorf("open runtime gzip stream: %w", err)
	}
	defer gz.Close()
	root, err := filepath.Abs(destination)
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("read runtime archive: %w", err)
		}
		target, err := safeRuntimeTarget(root, header.Name)
		if err != nil {
			return err
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			mode := os.FileMode(header.Mode) & 0o777
			if mode == 0 {
				mode = 0o644
			}
			if err := copyRuntimeFile(tr, target, mode); err != nil {
				return err
			}
		case tar.TypeSymlink:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			linkResolved := filepath.Clean(filepath.Join(filepath.Dir(target), filepath.FromSlash(strings.ReplaceAll(header.Linkname, "\\", "/"))))
			if linkResolved != root && !strings.HasPrefix(linkResolved, root+string(os.PathSeparator)) {
				return fmt.Errorf("runtime symlink escapes destination: %q", header.Name)
			}
			_ = os.Remove(target)
			if err := os.Symlink(header.Linkname, target); err != nil {
				return fmt.Errorf("create runtime symlink %q: %w", header.Name, err)
			}
		case tar.TypeLink:
			linkTarget, err := safeRuntimeTarget(root, header.Linkname)
			if err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			if err := os.Link(linkTarget, target); err != nil {
				return fmt.Errorf("create runtime hardlink %q: %w", header.Name, err)
			}
		default:
			// PAX/global metadata is handled by archive/tar. Ignore other
			// metadata-only records rather than treating them as files.
			if header.Typeflag != tar.TypeXHeader && header.Typeflag != tar.TypeXGlobalHeader {
				return fmt.Errorf("unsupported runtime archive entry %q", header.Name)
			}
		}
	}
	return nil
}

func safeRuntimeTarget(root, name string) (string, error) {
	clean := filepath.Clean(filepath.FromSlash(strings.ReplaceAll(name, "\\", "/")))
	if clean == "." {
		return root, nil
	}
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("unsafe runtime archive path %q", name)
	}
	target := filepath.Join(root, clean)
	resolved, err := filepath.Abs(target)
	if err != nil {
		return "", err
	}
	if resolved != root && !strings.HasPrefix(resolved, root+string(os.PathSeparator)) {
		return "", fmt.Errorf("runtime archive path escapes destination: %q", name)
	}
	return resolved, nil
}

func findRuntimeExecutable(root string, names []string) (string, error) {
	if _, err := os.Stat(root); err != nil {
		return "", err
	}
	wanted := map[string]bool{}
	for _, name := range names {
		wanted[strings.ToLower(name)] = true
	}
	var candidates []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		if wanted[strings.ToLower(entry.Name())] {
			if info, err := os.Stat(path); err == nil && !info.IsDir() {
				candidates = append(candidates, path)
			}
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if len(candidates) == 0 {
		return "", fmt.Errorf("runtime executable not found")
	}
	sort.Slice(candidates, func(i, j int) bool {
		iBin := strings.Contains(filepath.ToSlash(candidates[i]), "/bin/")
		jBin := strings.Contains(filepath.ToSlash(candidates[j]), "/bin/")
		if iBin != jBin {
			return iBin
		}
		return len(candidates[i]) < len(candidates[j])
	})
	return candidates[0], nil
}

func sanitizeVersion(value string) string {
	value = strings.TrimSpace(value)
	value = strings.ReplaceAll(value, "/", "-")
	value = strings.ReplaceAll(value, "\\", "-")
	return value
}

func extractNodeExecutable(archivePath, target, kind string) error {
	switch kind {
	case "zip":
		return extractNodeFromZIP(archivePath, target)
	case "tgz":
		return extractNodeFromTGZ(archivePath, target)
	default:
		return fmt.Errorf("unsupported Node archive format %q", kind)
	}
}

func extractNodeFromZIP(archivePath, target string) error {
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return fmt.Errorf("open Node zip: %w", err)
	}
	defer reader.Close()
	for _, file := range reader.File {
		name := strings.ReplaceAll(file.Name, "\\", "/")
		if !strings.HasSuffix(name, "/node.exe") && name != "node.exe" {
			continue
		}
		src, err := file.Open()
		if err != nil {
			return fmt.Errorf("open Node executable: %w", err)
		}
		err = copyRuntimeFile(src, target, 0o755)
		src.Close()
		return err
	}
	return fmt.Errorf("Node executable was not found in runtime archive")
}

func extractNodeFromTGZ(archivePath, target string) error {
	file, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("open Node archive: %w", err)
	}
	defer file.Close()
	gz, err := gzip.NewReader(file)
	if err != nil {
		return fmt.Errorf("open Node gzip stream: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("read Node archive: %w", err)
		}
		name := strings.ReplaceAll(header.Name, "\\", "/")
		if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA {
			continue
		}
		if !strings.HasSuffix(name, "/bin/node") && name != "bin/node" && name != "node" {
			continue
		}
		return copyRuntimeFile(tr, target, 0o755)
	}
	return fmt.Errorf("Node executable was not found in runtime archive")
}

func copyRuntimeFile(src io.Reader, target string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fmt.Errorf("create runtime directory: %w", err)
	}
	out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return fmt.Errorf("create runtime file: %w", err)
	}
	_, copyErr := io.Copy(out, src)
	closeErr := out.Close()
	if copyErr != nil {
		return fmt.Errorf("extract runtime file: %w", copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close runtime file: %w", closeErr)
	}
	return nil
}
