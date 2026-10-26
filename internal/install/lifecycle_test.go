package install

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
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
