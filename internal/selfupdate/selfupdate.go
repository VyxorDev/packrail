package selfupdate

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	versionURL  = "https://raw.githubusercontent.com/VyxorDev/packrail/main/VERSION"
	releaseBase = "https://github.com/VyxorDev/packrail/releases/download"
	maxBinary   = int64(64 << 20)
)

var httpClient = &http.Client{Timeout: 2 * time.Minute}

type Result struct {
	Previous string
	Current  string
	Updated  bool
	Asset    string
}

func Update(current string) (Result, error) {
	latest, err := fetchText(versionURL, 128)
	if err != nil {
		return Result{}, fmt.Errorf("check latest Packrail version: %w", err)
	}
	latest = strings.TrimSpace(strings.TrimPrefix(latest, "v"))
	if latest == "" {
		return Result{}, fmt.Errorf("latest Packrail version is empty")
	}
	cmp, err := compareVersions(current, latest)
	if err != nil {
		return Result{}, err
	}
	asset, err := assetName(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return Result{}, err
	}
	if cmp >= 0 {
		return Result{Previous: current, Current: current, Asset: asset}, nil
	}

	tag := "v" + latest
	checksums, err := fetchText(fmt.Sprintf("%s/%s/SHA256SUMS.txt", releaseBase, tag), 1<<20)
	if err != nil {
		return Result{}, fmt.Errorf("download release checksums: %w", err)
	}
	expected, err := checksumFor(checksums, asset)
	if err != nil {
		return Result{}, err
	}

	executable, err := os.Executable()
	if err != nil {
		return Result{}, fmt.Errorf("locate current executable: %w", err)
	}
	executable, err = filepath.Abs(executable)
	if err != nil {
		return Result{}, err
	}
	dir := filepath.Dir(executable)
	tmp, err := os.CreateTemp(dir, ".prl-update-*")
	if err != nil {
		return Result{}, fmt.Errorf("create update file: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()

	if err := downloadAndVerify(fmt.Sprintf("%s/%s/%s", releaseBase, tag, asset), expected, tmp); err != nil {
		tmp.Close()
		return Result{}, err
	}
	if err := tmp.Close(); err != nil {
		return Result{}, fmt.Errorf("close update file: %w", err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(tmpPath, 0o755); err != nil {
			return Result{}, fmt.Errorf("mark update executable: %w", err)
		}
	}

	backup := executable + ".packrail-old"
	_ = os.Remove(backup)
	if err := os.Rename(executable, backup); err != nil {
		return Result{}, fmt.Errorf("prepare executable replacement: %w", err)
	}
	if err := os.Rename(tmpPath, executable); err != nil {
		_ = os.Rename(backup, executable)
		return Result{}, fmt.Errorf("replace Packrail executable: %w", err)
	}
	cleanup = false
	_ = os.Remove(backup)
	return Result{Previous: current, Current: latest, Updated: true, Asset: asset}, nil
}

func assetName(goos, goarch string) (string, error) {
	arch := ""
	switch goarch {
	case "amd64":
		arch = "amd64"
	case "arm64":
		arch = "arm64"
	default:
		return "", fmt.Errorf("Packrail self-update does not support architecture %s", goarch)
	}
	switch goos {
	case "windows":
		return "prl-windows-" + arch + ".exe", nil
	case "linux":
		return "prl-linux-" + arch, nil
	case "darwin":
		return "prl-macos-" + arch, nil
	default:
		return "", fmt.Errorf("Packrail self-update does not support OS %s", goos)
	}
}

func checksumFor(manifest, asset string) (string, error) {
	scanner := bufio.NewScanner(strings.NewReader(manifest))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) >= 2 && strings.TrimPrefix(fields[len(fields)-1], "*") == asset {
			value := fields[0]
			if len(value) != 64 {
				return "", fmt.Errorf("invalid SHA-256 for %s", asset)
			}
			if _, err := hex.DecodeString(value); err != nil {
				return "", fmt.Errorf("invalid SHA-256 for %s", asset)
			}
			return strings.ToLower(value), nil
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return "", fmt.Errorf("release checksum for %s was not found", asset)
}

func downloadAndVerify(url, expected string, dst *os.File) error {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "Packrail/prl")
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("download update: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("download update: HTTP %s", resp.Status)
	}
	if resp.ContentLength > maxBinary {
		return fmt.Errorf("update binary exceeds %d bytes", maxBinary)
	}
	hash := sha256.New()
	limited := io.LimitReader(resp.Body, maxBinary+1)
	written, err := io.Copy(io.MultiWriter(dst, hash), limited)
	if err != nil {
		return fmt.Errorf("download update: %w", err)
	}
	if written > maxBinary {
		return fmt.Errorf("update binary exceeds %d bytes", maxBinary)
	}
	actual := hex.EncodeToString(hash.Sum(nil))
	if !strings.EqualFold(actual, expected) {
		return fmt.Errorf("update SHA-256 mismatch: expected %s, got %s", expected, actual)
	}
	if err := dst.Sync(); err != nil {
		return fmt.Errorf("sync update file: %w", err)
	}
	return nil
}

func fetchText(url string, limit int64) (string, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
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
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return "", err
	}
	if int64(len(data)) > limit {
		return "", fmt.Errorf("response exceeds %d bytes", limit)
	}
	return string(data), nil
}

func compareVersions(a, b string) (int, error) {
	acore, apre, err := splitVersion(a)
	if err != nil {
		return 0, fmt.Errorf("parse current version %q: %w", a, err)
	}
	bcore, bpre, err := splitVersion(b)
	if err != nil {
		return 0, fmt.Errorf("parse latest version %q: %w", b, err)
	}
	for i := 0; i < 3; i++ {
		if acore[i] < bcore[i] {
			return -1, nil
		}
		if acore[i] > bcore[i] {
			return 1, nil
		}
	}
	if apre == bpre {
		return 0, nil
	}
	if apre == "" {
		return 1, nil
	}
	if bpre == "" {
		return -1, nil
	}
	return strings.Compare(apre, bpre), nil
}

func splitVersion(value string) ([3]int, string, error) {
	var out [3]int
	value = strings.TrimSpace(strings.TrimPrefix(value, "v"))
	parts := strings.SplitN(value, "-", 2)
	numbers := strings.Split(parts[0], ".")
	if len(numbers) != 3 {
		return out, "", fmt.Errorf("expected major.minor.patch")
	}
	for i := range numbers {
		n, err := strconv.Atoi(numbers[i])
		if err != nil || n < 0 {
			return out, "", fmt.Errorf("invalid numeric component %q", numbers[i])
		}
		out[i] = n
	}
	pre := ""
	if len(parts) == 2 {
		pre = parts[1]
	}
	return out, pre, nil
}
