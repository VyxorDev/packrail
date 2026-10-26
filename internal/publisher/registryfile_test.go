package publisher

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/VyxorDev/packrail/internal/registry"
)

func TestUpsertRegistryFileAddsAndReplaces(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "packages.json")
	seed := []registry.Package{testRegistryPackage("alpha", "1.0.0")}
	data, _ := json.Marshal(seed)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}

	replaced, err := UpsertRegistryFile(path, testRegistryPackage("beta", "2.0.0"))
	if err != nil {
		t.Fatal(err)
	}
	if replaced {
		t.Fatal("expected beta to be added")
	}

	replaced, err = UpsertRegistryFile(path, testRegistryPackage("beta", "2.1.0"))
	if err != nil {
		t.Fatal(err)
	}
	if !replaced {
		t.Fatal("expected beta to be replaced")
	}

	final, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var packages []registry.Package
	if err := json.Unmarshal(final, &packages); err != nil {
		t.Fatal(err)
	}
	if len(packages) != 2 || packages[0].Name != "alpha" || packages[1].Name != "beta" || packages[1].Version != "2.1.0" {
		t.Fatalf("unexpected registry: %#v", packages)
	}
}

func testRegistryPackage(name, version string) registry.Package {
	return registry.Package{
		Name:        name,
		Version:     version,
		Description: "test package",
		Source:      "https://github.com/example/" + name,
		Artifacts: map[string]registry.Artifact{
			"any": {
				URL:        "https://github.com/example/" + name + "/releases/download/v" + version + "/" + name + ".tgz",
				SHA256:     "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
				Format:     "tgz",
				Entrypoint: "package/bin/tool.js",
			},
		},
		Command: registry.Command{Name: name},
	}
}
