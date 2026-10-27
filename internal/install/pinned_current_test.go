package install

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/VyxorDev/packrail/internal/registry"
	packruntimes "github.com/VyxorDev/packrail/internal/runtimes"
)

func TestIsCurrentRejectsRuntimeVersionDriftFromV2Pin(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PACKRAIL_HOME", home)

	spec := registry.Runtime{Name: "node", Constraint: ">=20", Command: "node"}
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
		Runtime:   spec,
		Artifacts: map[string]registry.Artifact{"any": artifact},
		Command:   registry.Command{Name: "demo"},
	}
	fingerprint, err := packageMetadataSHA256(pkg)
	if err != nil {
		t.Fatal(err)
	}

	platform := registry.PlatformKey(runtime.GOOS, runtime.GOARCH)
	executable := "node"
	format := "tgz"
	if runtime.GOOS == "windows" {
		executable = "node.exe"
		format = "zip"
	}
	packruntimes.ConfigureLockedRuntimes(map[string]packruntimes.LockedRuntime{
		packruntimes.SpecKey(spec): {
			Name:       "node",
			Constraint: spec.Constraint,
			Command:    spec.Command,
			Artifacts: map[string]packruntimes.Pin{
				platform: {
					Version:     "24.11.1",
					URL:         "https://example.com/node-runtime." + format,
					SHA256:      strings.Repeat("b", 64),
					Format:      format,
					Executables: []string{executable},
				},
			},
		},
	})
	defer packruntimes.ConfigureLockedRuntimes(nil)

	packageDir := filepath.Join(home, "packages", pkg.Name, pkg.Version)
	if err := os.MkdirAll(packageDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packageDir, "demo"), []byte("package"), 0o755); err != nil {
		t.Fatal(err)
	}
	binDir := filepath.Join(home, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	shim := filepath.Join(binDir, pkg.Command.Name)
	if runtime.GOOS == "windows" {
		shim += ".cmd"
	}
	if err := os.WriteFile(shim, []byte("shim"), 0o755); err != nil {
		t.Fatal(err)
	}

	writeRecordVersion := func(version string) {
		t.Helper()
		record := InstallRecord{
			Name:           pkg.Name,
			Version:        pkg.Version,
			Command:        pkg.Command.Name,
			Source:         pkg.Source,
			Platform:       platform,
			ArtifactSHA256: artifact.SHA256,
			MetadataSHA256: fingerprint,
			Runtime:        "node",
			RuntimeVersion: version,
			ManagedRuntime: true,
			Installed:      "2026-01-01T00:00:00Z",
		}
		data, err := json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(packageDir, ".packrail.json"), data, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(home, "runtimes", "node", version), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	writeRecordVersion("22.0.0")
	current, err := IsCurrent(pkg)
	if err != nil {
		t.Fatal(err)
	}
	if current {
		t.Fatal("install with stale runtime version was incorrectly considered current")
	}

	writeRecordVersion("24.11.1")
	current, err = IsCurrent(pkg)
	if err != nil {
		t.Fatal(err)
	}
	if !current {
		t.Fatal("install matching the exact locked runtime version should be current")
	}
}
