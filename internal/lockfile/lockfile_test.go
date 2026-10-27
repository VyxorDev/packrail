package lockfile

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/VyxorDev/packrail/internal/registry"
)

func testPackage(name, version string) registry.Package {
	return registry.Package{
		Name:        name,
		Version:     version,
		Description: "test tool",
		Source:      "https://github.com/example/" + strings.ToLower(name),
		Artifacts: map[string]registry.Artifact{
			"any": {
				URL:        "https://github.com/example/" + strings.ToLower(name) + "/releases/download/v1/tool.tgz",
				SHA256:     strings.Repeat("a", 64),
				Format:     "tgz",
				Entrypoint: "package/tool",
			},
		},
		Command: registry.Command{Name: strings.ToLower(name)},
	}
}

func TestUpsertSortsAndReplacesPackage(t *testing.T) {
	file := New()
	if err := Upsert(&file, testPackage("zeta", "1.0.0")); err != nil {
		t.Fatal(err)
	}
	if err := Upsert(&file, testPackage("alpha", "1.0.0")); err != nil {
		t.Fatal(err)
	}
	if err := Upsert(&file, testPackage("ALPHA", "2.0.0")); err != nil {
		t.Fatal(err)
	}
	if len(file.Packages) != 2 {
		t.Fatalf("got %d packages, want 2", len(file.Packages))
	}
	if file.Packages[0].Name != "alpha" || file.Packages[0].Version != "2.0.0" {
		t.Fatalf("unexpected first package: %#v", file.Packages[0])
	}
	if file.Packages[1].Name != "zeta" {
		t.Fatalf("unexpected second package: %#v", file.Packages[1])
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	want := New()
	if err := Upsert(&want, testPackage("demo", "1.2.3")); err != nil {
		t.Fatal(err)
	}
	if err := Save(path, want); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip mismatch:\n got: %#v\nwant: %#v", got, want)
	}
}

func TestLoadOrEmptyWhenMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	file, existed, err := LoadOrEmpty(path)
	if err != nil {
		t.Fatal(err)
	}
	if existed {
		t.Fatal("missing lockfile reported as existing")
	}
	if file.LockfileVersion != CurrentVersion || len(file.Packages) != 0 {
		t.Fatalf("unexpected empty lockfile: %#v", file)
	}
}

func TestLoadRejectsUnsupportedVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	if err := os.WriteFile(path, []byte("{\"lockfile_version\":2,\"packages\":[]}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "unsupported lockfile_version") {
		t.Fatalf("expected unsupported-version error, got %v", err)
	}
}

func TestLoadRejectsDuplicateNames(t *testing.T) {
	file := New()
	file.Packages = []registry.Package{
		testPackage("demo", "1.0.0"),
		testPackage("DEMO", "2.0.0"),
	}
	data, err := json.Marshal(file)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), FileName)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "duplicate package name") {
		t.Fatalf("expected duplicate-name error, got %v", err)
	}
}


func TestRemovePackageOnlyChangesLockfileEntries(t *testing.T) {
	file := New()
	if err := Upsert(&file, testPackage("alpha", "1.0.0")); err != nil {
		t.Fatal(err)
	}
	if err := Upsert(&file, testPackage("beta", "2.0.0")); err != nil {
		t.Fatal(err)
	}

	removed, err := Remove(&file, "ALPHA")
	if err != nil {
		t.Fatal(err)
	}
	if !removed {
		t.Fatal("expected alpha to be removed")
	}
	if len(file.Packages) != 1 || file.Packages[0].Name != "beta" {
		t.Fatalf("unexpected packages after removal: %#v", file.Packages)
	}
}

func TestRemoveMissingPackageReturnsFalse(t *testing.T) {
	file := New()
	if err := Upsert(&file, testPackage("alpha", "1.0.0")); err != nil {
		t.Fatal(err)
	}
	removed, err := Remove(&file, "missing")
	if err != nil {
		t.Fatal(err)
	}
	if removed {
		t.Fatal("missing package should not be reported as removed")
	}
	if len(file.Packages) != 1 || file.Packages[0].Name != "alpha" {
		t.Fatalf("lockfile changed after removing a missing package: %#v", file.Packages)
	}
}

func TestRemoveLastPackageKeepsEmptyArray(t *testing.T) {
	file := New()
	if err := Upsert(&file, testPackage("alpha", "1.0.0")); err != nil {
		t.Fatal(err)
	}
	removed, err := Remove(&file, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	if !removed {
		t.Fatal("expected alpha to be removed")
	}
	if file.Packages == nil || len(file.Packages) != 0 {
		t.Fatalf("expected an empty, non-nil package list; got %#v", file.Packages)
	}
}


func TestLoadRejectsPathTraversingVersion(t *testing.T) {
	file := New()
	file.Packages = []registry.Package{testPackage("demo", "../../outside")}
	data, err := json.Marshal(file)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), FileName)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "unsafe version") {
		t.Fatalf("expected unsafe version error, got %v", err)
	}
}

func TestLoadRejectsPathTraversingCommandName(t *testing.T) {
	pkg := testPackage("demo", "1.0.0")
	pkg.Command.Name = "../escape"
	file := New()
	file.Packages = []registry.Package{pkg}
	data, err := json.Marshal(file)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), FileName)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "unsafe command name") {
		t.Fatalf("expected unsafe command error, got %v", err)
	}
}
