package runtimes

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/VyxorDev/packrail/internal/registry"
)

const nodeDistBase = "https://nodejs.org/dist"

type Result struct {
	Executable string
	Version    string
	Managed    bool
}

type nodeRelease struct {
	Version string `json:"version"`
	LTS     any    `json:"lts"`
}

var httpClient = &http.Client{Timeout: 5 * time.Minute}

func Ensure(home string, spec registry.Runtime) (Result, error) {
	if strings.TrimSpace(spec.Name) == "" {
		return Result{}, nil
	}

	switch strings.ToLower(strings.TrimSpace(spec.Name)) {
	case "node", "nodejs":
		return ensureNode(home, spec.Constraint)
	default:
		return Result{}, fmt.Errorf("Packrail does not yet manage runtime %q", spec.Name)
	}
}

func InstalledVersions(home, name string) ([]string, error) {
	root := filepath.Join(home, "runtimes", strings.ToLower(name))
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

func ensureNode(home, constraint string) (Result, error) {
	minimum, err := minimumMajor(constraint)
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
	staging := filepath.Join(home, "staging")
	if err := os.MkdirAll(staging, 0o755); err != nil {
		return Result{}, fmt.Errorf("create runtime staging directory: %w", err)
	}
	tmp, err := os.CreateTemp(staging, "node-*")
	if err != nil {
		return Result{}, fmt.Errorf("create runtime download: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	url := fmt.Sprintf("%s/v%s/%s", nodeDistBase, version, asset)
	if err := downloadAndVerify(url, checksum, tmp); err != nil {
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

func fetchNodeIndex() ([]nodeRelease, error) {
	var releases []nodeRelease
	if err := getJSON(nodeDistBase+"/index.json", &releases); err != nil {
		return nil, fmt.Errorf("fetch Node release index: %w", err)
	}
	return releases, nil
}

func getJSON(url string, dst any) error {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "Packrail/prl")
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %s", resp.Status)
	}
	if err := json.NewDecoder(resp.Body).Decode(dst); err != nil {
		return err
	}
	return nil
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

	// Prefer an LTS line when one satisfies the package constraint. This keeps
	// managed runtimes conservative even when a newer Current line exists.
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

func minimumMajor(constraint string) (int, error) {
	value := strings.TrimSpace(constraint)
	value = strings.TrimPrefix(value, ">=")
	value = strings.TrimPrefix(value, "v")
	if i := strings.IndexByte(value, '.'); i >= 0 {
		value = value[:i]
	}
	major, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || major <= 0 {
		return 0, fmt.Errorf("unsupported constraint")
	}
	return major, nil
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

func fetchNodeChecksum(version, asset string) (string, error) {
	url := fmt.Sprintf("%s/v%s/SHASUMS256.txt", nodeDistBase, version)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Packrail/prl")
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch Node checksums: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("fetch Node checksums: HTTP %s", resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("read Node checksums: %w", err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[1] == asset {
			return fields[0], nil
		}
	}
	return "", fmt.Errorf("checksum for Node asset %s was not published", asset)
}

func downloadAndVerify(url, expected string, dst *os.File) error {
	req, err := http.NewRequest(http.MethodGet, url, nil)
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
	hash := sha256.New()
	if _, err := io.Copy(io.MultiWriter(dst, hash), resp.Body); err != nil {
		return fmt.Errorf("download runtime: %w", err)
	}
	actual := hex.EncodeToString(hash.Sum(nil))
	if !strings.EqualFold(actual, strings.TrimSpace(expected)) {
		return fmt.Errorf("runtime SHA-256 mismatch: expected %s, got %s", expected, actual)
	}
	return nil
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
		return fmt.Errorf("create runtime executable: %w", err)
	}
	_, copyErr := io.Copy(out, src)
	closeErr := out.Close()
	if copyErr != nil {
		return fmt.Errorf("extract runtime executable: %w", copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close runtime executable: %w", closeErr)
	}
	return nil
}
