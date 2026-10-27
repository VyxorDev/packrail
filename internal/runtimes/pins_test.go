package runtimes

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/VyxorDev/packrail/internal/registry"
)

func runtimeZip(t *testing.T, executable, contents string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	entry, err := writer.Create("runtime/bin/" + executable)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(entry, contents); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func sha256Text(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func TestEnsureLockedUsesExactPinInsteadOfConstraintResolver(t *testing.T) {
	executable := "node"
	if runtime.GOOS == "windows" {
		executable = "node.exe"
	}
	archive := runtimeZip(t, executable, "pinned-runtime")
	var requests int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requests, 1)
		_, _ = w.Write(archive)
	}))
	defer server.Close()

	oldClient := httpClient
	httpClient = server.Client()
	defer func() { httpClient = oldClient }()

	spec := registry.Runtime{Name: "node", Constraint: ">=999", Command: "node"}
	platform := registry.PlatformKey(runtime.GOOS, runtime.GOARCH)
	ConfigureLockedRuntimes(map[string]LockedRuntime{
		SpecKey(spec): {
			Name:       "node",
			Constraint: spec.Constraint,
			Command:    spec.Command,
			Artifacts: map[string]Pin{
				platform: {
					Version:     "24.11.1",
					URL:         server.URL + "/node.zip",
					SHA256:      sha256Text(archive),
					Format:      "zip",
					Executables: []string{executable},
				},
			},
		},
	})
	defer ConfigureLockedRuntimes(nil)

	home := t.TempDir()
	result, err := EnsureLocked(home, spec)
	if err != nil {
		t.Fatal(err)
	}
	if result.Version != "24.11.1" || !result.Managed {
		t.Fatalf("unexpected pinned runtime result: %#v", result)
	}
	data, err := os.ReadFile(result.Executable)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "pinned-runtime" {
		t.Fatalf("unexpected executable content: %q", data)
	}
	if atomic.LoadInt32(&requests) != 1 {
		t.Fatalf("download count = %d, want 1", requests)
	}

	// A second sync must reuse the exact installed pin and never consult the
	// resolver or download the runtime again.
	if _, err := EnsureLocked(home, spec); err != nil {
		t.Fatal(err)
	}
	if atomic.LoadInt32(&requests) != 1 {
		t.Fatalf("runtime was re-downloaded: %d requests", requests)
	}
}

func TestPinnedRuntimeChecksumFailurePreservesWorkingRuntime(t *testing.T) {
	executable := "node"
	if runtime.GOOS == "windows" {
		executable = "node.exe"
	}
	goodArchive := runtimeZip(t, executable, "known-good")
	badArchive := runtimeZip(t, executable, "corrupt-new-download")
	serveBad := false
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if serveBad {
			_, _ = w.Write(badArchive)
			return
		}
		_, _ = w.Write(goodArchive)
	}))
	defer server.Close()

	oldClient := httpClient
	httpClient = server.Client()
	defer func() { httpClient = oldClient }()

	spec := registry.Runtime{Name: "node", Constraint: ">=20", Command: "node"}
	platform := registry.PlatformKey(runtime.GOOS, runtime.GOARCH)
	goodPin := Pin{Version: "24.11.1", URL: server.URL + "/node.zip", SHA256: sha256Text(goodArchive), Format: "zip", Executables: []string{executable}}
	ConfigureLockedRuntimes(map[string]LockedRuntime{
		SpecKey(spec): {Name: "node", Constraint: spec.Constraint, Command: spec.Command, Artifacts: map[string]Pin{platform: goodPin}},
	})
	defer ConfigureLockedRuntimes(nil)

	home := t.TempDir()
	result, err := EnsureLocked(home, spec)
	if err != nil {
		t.Fatal(err)
	}
	workingPath := result.Executable

	// Force a pin identity mismatch so Packrail attempts a replacement, then
	// serve bytes that do not match the new checksum. The old runtime must stay.
	badPin := goodPin
	badPin.SHA256 = strings.Repeat("b", 64)
	ConfigureLockedRuntimes(map[string]LockedRuntime{
		SpecKey(spec): {Name: "node", Constraint: spec.Constraint, Command: spec.Command, Artifacts: map[string]Pin{platform: badPin}},
	})
	serveBad = true
	if _, err := EnsureLocked(home, spec); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("expected checksum failure, got %v", err)
	}
	data, err := os.ReadFile(workingPath)
	if err != nil {
		t.Fatalf("working runtime disappeared after failed replacement: %v", err)
	}
	if string(data) != "known-good" {
		t.Fatalf("working runtime changed after failed replacement: %q", data)
	}
	marker := filepath.Join(home, "runtimes", "node", goodPin.Version, platform, ".packrail-runtime.json")
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("working runtime marker disappeared: %v", err)
	}
}
