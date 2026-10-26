package install

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
	"strings"
	"time"

	"github.com/VyxorDev/packrail/internal/registry"
	packruntimes "github.com/VyxorDev/packrail/internal/runtimes"
)

type InstallRecord struct {
	Name           string `json:"name"`
	Version        string `json:"version"`
	Command        string `json:"command"`
	Source         string `json:"source"`
	Platform       string `json:"platform"`
	ArtifactSHA256 string `json:"artifact_sha256"`
	Runtime        string `json:"runtime,omitempty"`
	RuntimeVersion string `json:"runtime_version,omitempty"`
	ManagedRuntime bool   `json:"managed_runtime,omitempty"`
	Installed      string `json:"installed_at"`
}

func Home() (string, error) {
	if custom := strings.TrimSpace(os.Getenv("PACKRAIL_HOME")); custom != "" {
		return filepath.Abs(custom)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve user home: %w", err)
	}
	return filepath.Join(home, ".packrail"), nil
}

func Install(pkg registry.Package) error {
	artifact, err := registry.ArtifactFor(pkg, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return err
	}
	entrypoint, err := registry.EntrypointFor(pkg, artifact)
	if err != nil {
		return err
	}
	if artifact.Format != "tgz" && artifact.Format != "zip" {
		return fmt.Errorf("unsupported artifact format %q", artifact.Format)
	}

	home, err := Home()
	if err != nil {
		return err
	}
	packagesDir := filepath.Join(home, "packages")
	binDir := filepath.Join(home, "bin")
	stagingDir := filepath.Join(home, "staging")
	for _, dir := range []string{packagesDir, binDir, stagingDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create %s: %w", dir, err)
		}
	}

	runtimeResult, err := packruntimes.Ensure(home, pkg.Runtime)
	if err != nil {
		return fmt.Errorf("prepare %s runtime: %w", pkg.Runtime.Name, err)
	}
	return installArtifact(pkg, artifact, entrypoint, runtimeResult, packagesDir, binDir, stagingDir)
}

func installArtifact(pkg registry.Package, artifact registry.Artifact, entrypoint string, runtimeResult packruntimes.Result, packagesDir, binDir, stagingDir string) error {
	suffix := ".tgz"
	if artifact.Format == "zip" {
		suffix = ".zip"
	}
	tmp, err := os.CreateTemp(stagingDir, pkg.Name+"-*"+suffix)
	if err != nil {
		return fmt.Errorf("create temporary download: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if err := downloadAndVerify(artifact.URL, artifact.SHA256, tmp); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close download: %w", err)
	}

	finalDir := filepath.Join(packagesDir, pkg.Name, pkg.Version)
	stagePackageDir, err := os.MkdirTemp(stagingDir, pkg.Name+"-extract-*")
	if err != nil {
		return fmt.Errorf("create extraction directory: %w", err)
	}
	defer os.RemoveAll(stagePackageDir)

	if err := extractArchive(tmpPath, stagePackageDir, artifact.Format); err != nil {
		return err
	}
	entry := filepath.Join(stagePackageDir, filepath.FromSlash(entrypoint))
	if info, err := os.Stat(entry); err != nil || info.IsDir() {
		return fmt.Errorf("package entrypoint %q was not found in artifact", entrypoint)
	}
	if runtimeResult.Executable == "" && runtime.GOOS != "windows" {
		if err := os.Chmod(entry, 0o755); err != nil {
			return fmt.Errorf("mark native entrypoint executable: %w", err)
		}
	}

	if err := os.MkdirAll(filepath.Dir(finalDir), 0o755); err != nil {
		return fmt.Errorf("create package directory: %w", err)
	}
	if err := os.RemoveAll(finalDir); err != nil {
		return fmt.Errorf("replace existing installation: %w", err)
	}
	if err := os.Rename(stagePackageDir, finalDir); err != nil {
		return fmt.Errorf("commit installation: %w", err)
	}

	if err := writeRecord(finalDir, pkg, artifact, runtimeResult); err != nil {
		return err
	}
	if err := writeShim(binDir, finalDir, pkg, entrypoint, runtimeResult.Executable); err != nil {
		return err
	}
	return nil
}

func downloadAndVerify(url, expected string, dst *os.File) error {
	client := &http.Client{Timeout: 2 * time.Minute}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("create artifact request: %w", err)
	}
	req.Header.Set("User-Agent", "Packrail/prl")
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("download artifact: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("download artifact: HTTP %s", resp.Status)
	}

	hash := sha256.New()
	if _, err := io.Copy(io.MultiWriter(dst, hash), resp.Body); err != nil {
		return fmt.Errorf("download artifact: %w", err)
	}
	actual := hex.EncodeToString(hash.Sum(nil))
	if !strings.EqualFold(actual, strings.TrimSpace(expected)) {
		return fmt.Errorf("SHA-256 mismatch: expected %s, got %s", expected, actual)
	}
	return nil
}

func extractArchive(archivePath, destination, format string) error {
	switch format {
	case "tgz":
		return extractTGZ(archivePath, destination)
	case "zip":
		return extractZIP(archivePath, destination)
	default:
		return fmt.Errorf("unsupported artifact format %q", format)
	}
}

func extractTGZ(archivePath, destination string) error {
	file, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("open archive: %w", err)
	}
	defer file.Close()
	gz, err := gzip.NewReader(file)
	if err != nil {
		return fmt.Errorf("open gzip stream: %w", err)
	}
	defer gz.Close()

	root, err := filepath.Abs(destination)
	if err != nil {
		return fmt.Errorf("resolve extraction root: %w", err)
	}
	tr := tar.NewReader(gz)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("read archive: %w", err)
		}
		resolved, err := safeArchiveTarget(root, header.Name)
		if err != nil {
			return err
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(resolved, 0o755); err != nil {
				return fmt.Errorf("create directory %q: %w", header.Name, err)
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(resolved), 0o755); err != nil {
				return fmt.Errorf("create parent directory: %w", err)
			}
			mode := os.FileMode(header.Mode) & 0o777
			if mode == 0 {
				mode = 0o644
			}
			out, err := os.OpenFile(resolved, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
			if err != nil {
				return fmt.Errorf("create extracted file %q: %w", header.Name, err)
			}
			_, copyErr := io.Copy(out, tr)
			closeErr := out.Close()
			if copyErr != nil {
				return fmt.Errorf("extract file %q: %w", header.Name, copyErr)
			}
			if closeErr != nil {
				return fmt.Errorf("close extracted file %q: %w", header.Name, closeErr)
			}
		case tar.TypeSymlink, tar.TypeLink:
			return fmt.Errorf("links are not allowed in Packrail archives: %q", header.Name)
		default:
			return fmt.Errorf("unsupported archive entry type for %q", header.Name)
		}
	}
	return nil
}

func extractZIP(archivePath, destination string) error {
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return fmt.Errorf("open zip archive: %w", err)
	}
	defer reader.Close()
	root, err := filepath.Abs(destination)
	if err != nil {
		return fmt.Errorf("resolve extraction root: %w", err)
	}

	for _, file := range reader.File {
		resolved, err := safeArchiveTarget(root, file.Name)
		if err != nil {
			return err
		}
		info := file.FileInfo()
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("links are not allowed in Packrail archives: %q", file.Name)
		}
		if info.IsDir() {
			if err := os.MkdirAll(resolved, 0o755); err != nil {
				return fmt.Errorf("create directory %q: %w", file.Name, err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(resolved), 0o755); err != nil {
			return fmt.Errorf("create parent directory: %w", err)
		}
		src, err := file.Open()
		if err != nil {
			return fmt.Errorf("open zip entry %q: %w", file.Name, err)
		}
		mode := info.Mode().Perm()
		if mode == 0 {
			mode = 0o644
		}
		out, err := os.OpenFile(resolved, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
		if err != nil {
			src.Close()
			return fmt.Errorf("create extracted file %q: %w", file.Name, err)
		}
		_, copyErr := io.Copy(out, src)
		closeOutErr := out.Close()
		closeSrcErr := src.Close()
		if copyErr != nil {
			return fmt.Errorf("extract file %q: %w", file.Name, copyErr)
		}
		if closeOutErr != nil {
			return fmt.Errorf("close extracted file %q: %w", file.Name, closeOutErr)
		}
		if closeSrcErr != nil {
			return fmt.Errorf("close zip entry %q: %w", file.Name, closeSrcErr)
		}
	}
	return nil
}

func safeArchiveTarget(root, name string) (string, error) {
	normalized := strings.ReplaceAll(name, "\\", "/")
	cleanName := filepath.Clean(filepath.FromSlash(normalized))
	if cleanName == "." || filepath.IsAbs(cleanName) || cleanName == ".." || strings.HasPrefix(cleanName, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("unsafe archive path %q", name)
	}
	target := filepath.Join(root, cleanName)
	resolved, err := filepath.Abs(target)
	if err != nil {
		return "", fmt.Errorf("resolve archive path: %w", err)
	}
	if resolved != root && !strings.HasPrefix(resolved, root+string(os.PathSeparator)) {
		return "", fmt.Errorf("archive path escapes destination: %q", name)
	}
	return resolved, nil
}

func writeRecord(finalDir string, pkg registry.Package, artifact registry.Artifact, runtimeResult packruntimes.Result) error {
	record := InstallRecord{
		Name:           pkg.Name,
		Version:        pkg.Version,
		Command:        pkg.Command.Name,
		Source:         pkg.Source,
		Platform:       registry.PlatformKey(runtime.GOOS, runtime.GOARCH),
		ArtifactSHA256: artifact.SHA256,
		Runtime:        pkg.Runtime.Name,
		RuntimeVersion: runtimeResult.Version,
		ManagedRuntime: runtimeResult.Managed,
		Installed:      time.Now().UTC().Format(time.RFC3339),
	}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return fmt.Errorf("encode install record: %w", err)
	}
	if err := os.WriteFile(filepath.Join(finalDir, ".packrail.json"), append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("write install record: %w", err)
	}
	return nil
}

func writeShim(binDir, finalDir string, pkg registry.Package, entrypoint, runtimeExecutable string) error {
	entry := filepath.Join(finalDir, filepath.FromSlash(entrypoint))
	program := entry
	if runtimeExecutable != "" {
		program = runtimeExecutable
	}
	args := launchArgs(pkg.Command.Args, entry, runtimeExecutable != "")

	if runtime.GOOS == "windows" {
		shim := filepath.Join(binDir, pkg.Command.Name+".cmd")
		parts := []string{"@echo off\r\n", cmdQuote(program)}
		for _, arg := range args {
			parts = append(parts, " ", cmdQuote(arg))
		}
		parts = append(parts, " %*\r\n")
		if err := os.WriteFile(shim, []byte(strings.Join(parts, "")), 0o755); err != nil {
			return fmt.Errorf("write Windows shim: %w", err)
		}
		return nil
	}

	shim := filepath.Join(binDir, pkg.Command.Name)
	parts := []string{"#!/bin/sh\nexec ", shellQuote(program)}
	for _, arg := range args {
		parts = append(parts, " ", shellQuote(arg))
	}
	parts = append(parts, " \"$@\"\n")
	if err := os.WriteFile(shim, []byte(strings.Join(parts, "")), 0o755); err != nil {
		return fmt.Errorf("write Unix shim: %w", err)
	}
	return nil
}

func launchArgs(configured []string, entry string, hasRuntime bool) []string {
	if len(configured) == 0 {
		if hasRuntime {
			return []string{entry}
		}
		return nil
	}
	args := make([]string, 0, len(configured)+1)
	foundEntrypoint := false
	for _, arg := range configured {
		if arg == "{entrypoint}" {
			args = append(args, entry)
			foundEntrypoint = true
		} else {
			args = append(args, arg)
		}
	}
	if hasRuntime && !foundEntrypoint {
		args = append(args, entry)
	}
	return args
}

func cmdQuote(value string) string {
	return "\"" + strings.ReplaceAll(value, "\"", "\"\"") + "\""
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
