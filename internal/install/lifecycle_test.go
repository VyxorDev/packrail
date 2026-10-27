package install

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/VyxorDev/packrail/internal/registry"
)

func TestRemoveUsesInstallRecords(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PACKRAIL_HOME", home)
	packageDir := filepath.Join(home, "packages", "demo", "1.0.0")
	if err := os.MkdirAll(packageDir, 0o755); err != nil {
		t.Fatal(err)
	}
	record := InstallRecord{Name: "demo", Version: "1.0.0", Command: "demo"}
	data, _ := json.Marshal(record)
	if err := os.WriteFile(filepath.Join(packageDir, ".packrail.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(home, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "demo"), []byte("shim"), 0o755); err != nil {
		t.Fatal(err)
	}

	removed, err := Remove("demo")
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 1 || removed[0].Version != "1.0.0" {
		t.Fatalf("unexpected removed records: %#v", removed)
	}
	if _, err := os.Stat(filepath.Join(home, "packages", "demo")); !os.IsNotExist(err) {
		t.Fatalf("package directory still exists: %v", err)
	}
	if _, err := os.Stat(filepath.Join(bin, "demo")); !os.IsNotExist(err) {
		t.Fatalf("shim still exists: %v", err)
	}
}


func TestIsCurrentUsesPackageMetadataFingerprint(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PACKRAIL_HOME", home)

	artifact := registry.Artifact{
		URL:        "https://github.com/example/demo/releases/download/v1/demo.tgz",
		SHA256:     strings.Repeat("a", 64),
		Format:     "tgz",
		Entrypoint: "demo",
	}
	pkg := registry.Package{
		Name:      "demo",
		Version:   "1.0.0",
		Source:    "https://github.com/example/demo",
		Artifacts: map[string]registry.Artifact{"any": artifact},
		Command:   registry.Command{Name: "demo"},
	}
	fingerprint, err := packageMetadataSHA256(pkg)
	if err != nil {
		t.Fatal(err)
	}

	packageDir := filepath.Join(home, "packages", "demo", "1.0.0")
	if err := os.MkdirAll(packageDir, 0o755); err != nil {
		t.Fatal(err)
	}
	record := InstallRecord{
		Name:           pkg.Name,
		Version:        pkg.Version,
		Command:        pkg.Command.Name,
		Platform:       registry.PlatformKey(runtime.GOOS, runtime.GOARCH),
		ArtifactSHA256: artifact.SHA256,
		MetadataSHA256: fingerprint,
	}
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
	shimName := pkg.Command.Name
	if runtime.GOOS == "windows" {
		shimName += ".cmd"
	}
	if err := os.WriteFile(filepath.Join(binDir, shimName), []byte("shim"), 0o755); err != nil {
		t.Fatal(err)
	}

	current, err := IsCurrent(pkg)
	if err != nil {
		t.Fatal(err)
	}
	if !current {
		t.Fatal("matching install should be considered current")
	}

	pkg.Command.Args = []string{"--changed"}
	current, err = IsCurrent(pkg)
	if err != nil {
		t.Fatal(err)
	}
	if current {
		t.Fatal("changed package metadata must invalidate the installed record")
	}
}
