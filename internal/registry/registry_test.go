package registry

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFindStarterQueue(t *testing.T) {
	t.Setenv("PACKRAIL_HOME", t.TempDir())
	pkg, err := Find("starterqueue")
	if err != nil {
		t.Fatal(err)
	}
	if pkg.Version != "0.5.0" {
		t.Fatalf("version = %q, want 0.5.0", pkg.Version)
	}
	artifact, err := ArtifactFor(pkg, "windows", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	if artifact.SHA256 == "" {
		t.Fatal("expected SHA-256 to be mandatory")
	}
	if artifact.Format != "tgz" {
		t.Fatalf("format = %q, want tgz", artifact.Format)
	}
}

func TestFindRipgrepPlatformArtifact(t *testing.T) {
	t.Setenv("PACKRAIL_HOME", t.TempDir())
	pkg, err := Find("ripgrep")
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := ArtifactFor(pkg, "windows", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	if artifact.Format != "zip" {
		t.Fatalf("format = %q, want zip", artifact.Format)
	}
	if artifact.Entrypoint != "ripgrep-15.2.0-x86_64-pc-windows-msvc/rg.exe" {
		t.Fatalf("unexpected entrypoint: %q", artifact.Entrypoint)
	}
}

func TestArtifactForUnsupportedPlatform(t *testing.T) {
	t.Setenv("PACKRAIL_HOME", t.TempDir())
	pkg, err := Find("ripgrep")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ArtifactFor(pkg, "plan9", "amd64"); err == nil {
		t.Fatal("expected unsupported platform error")
	}
}

func TestEntrypointForRejectsUnsafePaths(t *testing.T) {
	pkg := Package{Name: "demo", Command: Command{Name: "demo"}}
	unsafe := []string{
		"../outside",
		"../../outside",
		"/etc/passwd",
		"C:\\Windows\\system32",
		"\\\\server\\share\\tool",
		"folder/../../outside",
	}
	for _, entrypoint := range unsafe {
		if _, err := EntrypointFor(pkg, Artifact{Entrypoint: entrypoint}); err == nil {
			t.Errorf("EntrypointFor(%q) unexpectedly succeeded", entrypoint)
		}
	}
}

func TestDecodeRejectsUnsafeEntrypoint(t *testing.T) {
	manifest := strings.Replace(
		testRegistryJSON("unsafe-tool", "1.0.0"),
		"\"entrypoint\": \"package/tool\"",
		"\"entrypoint\": \"../escape\"",
		1,
	)
	if _, err := decodeAndValidate([]byte(manifest)); err == nil {
		t.Fatal("expected unsafe entrypoint to be rejected")
	}
}

func TestSearch(t *testing.T) {
	t.Setenv("PACKRAIL_HOME", t.TempDir())
	matches, err := Search("queue")
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 || matches[0].Name != "starterqueue" {
		t.Fatalf("unexpected matches: %#v", matches)
	}

	matches, err = Search("ripgrep")
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 || matches[0].Name != "ripgrep" {
		t.Fatalf("unexpected ripgrep matches: %#v", matches)
	}
}

func TestSyncMakesRemoteRegistryAuthoritative(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PACKRAIL_HOME", home)

	manifest := testRegistryJSON("remote-tool", "9.1.0")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(manifest))
	}))
	defer server.Close()
	t.Setenv("PACKRAIL_REGISTRY_URL", server.URL)

	status, err := Sync()
	if err != nil {
		t.Fatal(err)
	}
	if status.Source != "cache" || status.PackageCount != 1 {
		t.Fatalf("unexpected sync status: %#v", status)
	}

	pkg, err := Find("remote-tool")
	if err != nil {
		t.Fatal(err)
	}
	if pkg.Version != "9.1.0" {
		t.Fatalf("remote version = %q, want 9.1.0", pkg.Version)
	}
	if _, err := Find("starterqueue"); err == nil {
		t.Fatal("embedded registry should not override a valid synced cache")
	}
}

func TestFailedSyncPreservesLastValidCache(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PACKRAIL_HOME", home)

	body := testRegistryJSON("stable-tool", "1.0.0")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()
	t.Setenv("PACKRAIL_REGISTRY_URL", server.URL)

	if _, err := Sync(); err != nil {
		t.Fatal(err)
	}
	body = `[{"name":"broken","version":"2","source":"http://insecure.example","artifacts":{},"command":{"name":"broken"}}]`
	if _, err := Sync(); err == nil {
		t.Fatal("expected malformed remote registry to be rejected")
	}

	pkg, err := Find("stable-tool")
	if err != nil {
		t.Fatal(err)
	}
	if pkg.Version != "1.0.0" {
		t.Fatalf("cache changed after failed sync: %#v", pkg)
	}
}

func TestDecodeRejectsInvalidChecksum(t *testing.T) {
	manifest := strings.Replace(testRegistryJSON("bad-hash", "1.0.0"), strings.Repeat("a", 64), "abc", 1)
	if _, err := decodeAndValidate([]byte(manifest)); err == nil {
		t.Fatal("expected invalid checksum to be rejected")
	}
}

func testRegistryJSON(name, version string) string {
	return `[
  {
    "name": "` + name + `",
    "version": "` + version + `",
    "description": "test package",
    "source": "https://github.com/example/tool",
    "artifacts": {
      "any": {
        "url": "https://github.com/example/tool/releases/download/v1/tool.tgz",
        "sha256": "` + strings.Repeat("a", 64) + `",
        "format": "tgz"
      }
    },
    "command": {
      "name": "tool",
      "entrypoint": "package/tool"
    }
  }
]`
}


func TestDecodeRejectsUnsafeVersionPath(t *testing.T) {
	manifest := testRegistryJSON("unsafe-version", "../../outside")
	if _, err := decodeAndValidate([]byte(manifest)); err == nil {
		t.Fatal("expected path-traversing version to be rejected")
	}
}

func TestDecodeRejectsUnsafeCommandPath(t *testing.T) {
	manifest := strings.Replace(
		testRegistryJSON("unsafe-command", "1.0.0"),
		"\"name\": \"tool\"",
		"\"name\": \"../escape\"",
		1,
	)
	if _, err := decodeAndValidate([]byte(manifest)); err == nil {
		t.Fatal("expected path-traversing command name to be rejected")
	}
}
