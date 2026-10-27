package install

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/VyxorDev/packrail/internal/registry"
)

func seedInstalledPackage(t *testing.T, home, name, version, command string) (string, string) {
	t.Helper()
	packageDir := filepath.Join(home, "packages", name, version)
	if err := os.MkdirAll(packageDir, 0o755); err != nil {
		t.Fatal(err)
	}
	entry := filepath.Join(packageDir, command)
	if err := os.WriteFile(entry, []byte("known-good-package"), 0o755); err != nil {
		t.Fatal(err)
	}
	record := InstallRecord{Name: name, Version: version, Command: command, Platform: registry.PlatformKey(runtime.GOOS, runtime.GOARCH), Installed: "2026-01-01T00:00:00Z"}
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packageDir, ".packrail.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	binDir := filepath.Join(home, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	shim := filepath.Join(binDir, command)
	if runtime.GOOS == "windows" {
		shim += ".cmd"
	}
	if err := os.WriteFile(shim, []byte("known-good-shim"), 0o755); err != nil {
		t.Fatal(err)
	}
	return entry, shim
}

func packageForRecovery(name, version, command, artifactURL, checksum string) registry.Package {
	return registry.Package{
		Name:    name,
		Version: version,
		Source:  "https://github.com/example/" + name,
		Artifacts: map[string]registry.Artifact{
			"any": {URL: artifactURL, SHA256: checksum, Format: "tgz", Entrypoint: command},
		},
		Command: registry.Command{Name: command},
	}
}

func TestFailedChecksumUpdateKeepsExistingPackageAndShim(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PACKRAIL_HOME", home)
	entry, shim := seedInstalledPackage(t, home, "demo", "1.0.0", "demo")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("not-the-expected-archive"))
	}))
	defer server.Close()

	pkg := packageForRecovery("demo", "2.0.0", "demo", server.URL+"/demo.tgz", strings.Repeat("a", 64))
	if _, _, err := Update(pkg); err == nil || !strings.Contains(err.Error(), "SHA-256 mismatch") {
		t.Fatalf("expected checksum failure, got %v", err)
	}
	entryData, err := os.ReadFile(entry)
	if err != nil {
		t.Fatalf("working package disappeared: %v", err)
	}
	if string(entryData) != "known-good-package" {
		t.Fatalf("working package changed: %q", entryData)
	}
	shimData, err := os.ReadFile(shim)
	if err != nil {
		t.Fatalf("working shim disappeared: %v", err)
	}
	if string(shimData) != "known-good-shim" {
		t.Fatalf("working shim changed: %q", shimData)
	}
	if _, err := os.Stat(filepath.Join(home, "packages", "demo", "2.0.0")); !os.IsNotExist(err) {
		t.Fatalf("failed version was committed: %v", err)
	}
}

func maliciousTGZ(t *testing.T) []byte {
	t.Helper()
	var buffer bytes.Buffer
	gz := gzip.NewWriter(&buffer)
	tw := tar.NewWriter(gz)
	payload := []byte("escape")
	if err := tw.WriteHeader(&tar.Header{Name: "../escape", Mode: 0o644, Size: int64(len(payload)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func TestFailedExtractionUpdateKeepsExistingPackage(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PACKRAIL_HOME", home)
	entry, _ := seedInstalledPackage(t, home, "demo", "1.0.0", "demo")
	archive := maliciousTGZ(t)
	sum := sha256.Sum256(archive)
	checksum := hex.EncodeToString(sum[:])

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(archive)
	}))
	defer server.Close()

	pkg := packageForRecovery("demo", "2.0.0", "demo", server.URL+"/demo.tgz", checksum)
	if _, _, err := Update(pkg); err == nil || !strings.Contains(err.Error(), "unsafe archive path") {
		t.Fatalf("expected extraction failure, got %v", err)
	}
	data, err := os.ReadFile(entry)
	if err != nil {
		t.Fatalf("working package disappeared: %v", err)
	}
	if string(data) != "known-good-package" {
		t.Fatalf("working package changed: %q", data)
	}
	if _, err := os.Stat(filepath.Join(home, "packages", "demo", "2.0.0")); !os.IsNotExist(err) {
		t.Fatalf("failed version was committed: %v", err)
	}
}
