package registry

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testPackage(name, version, source string) Package {
	return Package{
		Name:        name,
		Version:     version,
		Description: "test package",
		Source:      source,
		Artifacts: map[string]Artifact{
			"any": {
				URL:        "https://example.com/tool.zip",
				SHA256:     strings.Repeat("a", 64),
				Format:     "zip",
				Entrypoint: "tool",
			},
		},
		Command: Command{Name: "tool"},
	}
}

func TestCatalogBuildAndValidate(t *testing.T) {
	dir := t.TempDir()
	manifestDir := filepath.Join(dir, "registry", "packages")
	indexPath := filepath.Join(dir, "registry", "index.json")

	if _, err := WriteManifest(manifestDir, testPackage("beta", "1.0.0", "https://github.com/example/beta")); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteManifest(manifestDir, testPackage("alpha", "2.0.0", "https://github.com/example/alpha")); err != nil {
		t.Fatal(err)
	}
	count, err := BuildIndex(manifestDir, indexPath)
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("count = %d, want 2", count)
	}
	if _, err := ValidateIndex(manifestDir, indexPath); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Index(string(data), `"name": "alpha"`) > strings.Index(string(data), `"name": "beta"`) {
		t.Fatal("index is not sorted by package name")
	}
}

func TestCatalogRejectsWrongFilename(t *testing.T) {
	dir := t.TempDir()
	data, err := EncodePackage(testPackage("alpha", "1.0.0", "https://github.com/example/alpha"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "wrong.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadCatalog(dir); err == nil || !strings.Contains(err.Error(), "must be named alpha.json") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateMutationRejectsSourceChange(t *testing.T) {
	oldPkg := testPackage("alpha", "1.0.0", "https://github.com/example/alpha")
	newPkg := testPackage("alpha", "2.0.0", "https://github.com/attacker/alpha")
	if err := ValidateMutation(oldPkg, newPkg); err == nil || !strings.Contains(err.Error(), "source cannot change") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateMutationRejectsSameVersionRewrite(t *testing.T) {
	oldPkg := testPackage("alpha", "1.0.0", "https://github.com/example/alpha")
	newPkg := oldPkg
	newPkg.Description = "changed"
	if err := ValidateMutation(oldPkg, newPkg); err == nil || !strings.Contains(err.Error(), "published versions are immutable") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateMutationAllowsNewVersion(t *testing.T) {
	oldPkg := testPackage("alpha", "1.0.0", "https://github.com/example/alpha")
	newPkg := testPackage("alpha", "1.1.0", "https://github.com/example/alpha")
	if err := ValidateMutation(oldPkg, newPkg); err != nil {
		t.Fatal(err)
	}
}
