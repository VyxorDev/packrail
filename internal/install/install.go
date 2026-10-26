package install

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/VyxorDev/packrail/internal/registry"
)

type InstallRecord struct {
	Name      string `json:"name"`
	Version   string `json:"version"`
	Command   string `json:"command"`
	Source    string `json:"source"`
	Installed string `json:"installed_at"`
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
	if pkg.Runtime.Command != "" {
		if _, err := exec.LookPath(pkg.Runtime.Command); err != nil {
			return fmt.Errorf("required runtime %s %s is not available on PATH", pkg.Runtime.Name, pkg.Runtime.Constraint)
		}
	}
	if pkg.Artifact.Format != "tgz" {
		return fmt.Errorf("unsupported artifact format %q", pkg.Artifact.Format)
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

	tmp, err := os.CreateTemp(stagingDir, pkg.Name+"-*.tgz")
	if err != nil {
		return fmt.Errorf("create temporary download: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if err := downloadAndVerify(pkg.Artifact.URL, pkg.Artifact.SHA256, tmp); err != nil {
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

	if err := extractTGZ(tmpPath, stagePackageDir); err != nil {
		return err
	}
	entry := filepath.Join(stagePackageDir, filepath.FromSlash(pkg.Command.Entrypoint))
	if info, err := os.Stat(entry); err != nil || info.IsDir() {
		return fmt.Errorf("package entrypoint %q was not found in artifact", pkg.Command.Entrypoint)
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

	if err := writeRecord(finalDir, pkg); err != nil {
		return err
	}
	if err := writeShim(binDir, finalDir, pkg); err != nil {
		return err
	}
	return nil
}

func downloadAndVerify(url, expected string, dst *os.File) error {
	client := &http.Client{Timeout: 2 * time.Minute}
	resp, err := client.Get(url)
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

		cleanName := filepath.Clean(filepath.FromSlash(header.Name))
		if cleanName == "." || filepath.IsAbs(cleanName) || cleanName == ".." || strings.HasPrefix(cleanName, ".."+string(os.PathSeparator)) {
			return fmt.Errorf("unsafe archive path %q", header.Name)
		}
		target := filepath.Join(root, cleanName)
		resolved, err := filepath.Abs(target)
		if err != nil {
			return fmt.Errorf("resolve archive path: %w", err)
		}
		if resolved != root && !strings.HasPrefix(resolved, root+string(os.PathSeparator)) {
			return fmt.Errorf("archive path escapes destination: %q", header.Name)
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
			out, err := os.OpenFile(resolved, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(header.Mode)&0o777)
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

func writeRecord(finalDir string, pkg registry.Package) error {
	record := InstallRecord{
		Name:      pkg.Name,
		Version:   pkg.Version,
		Command:   pkg.Command.Name,
		Source:    pkg.Source,
		Installed: time.Now().UTC().Format(time.RFC3339),
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

func writeShim(binDir, finalDir string, pkg registry.Package) error {
	entry := filepath.Join(finalDir, filepath.FromSlash(pkg.Command.Entrypoint))
	if runtime.GOOS == "windows" {
		shim := filepath.Join(binDir, pkg.Command.Name+".cmd")
		content := fmt.Sprintf("@echo off\r\n%s \"%s\" %%*\r\n", pkg.Runtime.Command, entry)
		if err := os.WriteFile(shim, []byte(content), 0o755); err != nil {
			return fmt.Errorf("write Windows shim: %w", err)
		}
		return nil
	}

	shim := filepath.Join(binDir, pkg.Command.Name)
	content := fmt.Sprintf("#!/bin/sh\nexec %s %q \"$@\"\n", pkg.Runtime.Command, entry)
	if err := os.WriteFile(shim, []byte(content), 0o755); err != nil {
		return fmt.Errorf("write Unix shim: %w", err)
	}
	return nil
}
