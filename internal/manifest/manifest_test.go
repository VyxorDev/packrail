package manifest

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

func TestDecodeShorthandAndObjectDependencies(t *testing.T) {
	data := []byte(`{
  "manifest_version": 1,
  "dependencies": {
    "ripgrep": "^15",
    "starterqueue": {"constraint": ">=0.5", "repo": "VyxorDev/starterqueue"}
  }
}`)
	file, err := Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	if got := file.Dependencies["ripgrep"].Constraint; got != "^15" {
		t.Fatalf("ripgrep constraint = %q", got)
	}
	dep := file.Dependencies["starterqueue"]
	if dep.Constraint != ">=0.5" || dep.Repo != "VyxorDev/starterqueue" {
		t.Fatalf("unexpected starterqueue dependency: %#v", dep)
	}
}

func TestDecodeRejectsUnknownAndMalformedFields(t *testing.T) {
	cases := []string{
		`{"manifest_version":1,"dependencies":{},"surprise":true}`,
		`{"manifest_version":1,"dependencies":{"demo":{"constraint":"*","surprise":true}}}`,
		`{"manifest_version":1,"dependencies":`,
		`{"manifest_version":1,"dependencies":{}} {}`,
	}
	for _, input := range cases {
		if _, err := Decode([]byte(input)); err == nil {
			t.Fatalf("expected decode failure for %s", input)
		}
	}
}

func TestValidateRejectsUnsafeDependencyData(t *testing.T) {
	cases := []File{
		{ManifestVersion: 1, Dependencies: map[string]Dependency{"../escape": {Constraint: "*"}}},
		{ManifestVersion: 1, Dependencies: map[string]Dependency{"demo": {Constraint: ">1"}}},
		{ManifestVersion: 1, Dependencies: map[string]Dependency{"demo": {Constraint: "*", Repo: "../../repo"}}},
		{ManifestVersion: 1, Dependencies: map[string]Dependency{"demo": {Constraint: "*", Repo: "../repo"}}},
		{ManifestVersion: 1, Dependencies: map[string]Dependency{"demo": {Constraint: "*", Repo: "owner/.."}}},
	}
	for _, file := range cases {
		if err := Validate(&file); err == nil {
			t.Fatalf("expected invalid manifest: %#v", file)
		}
	}
}

func TestEncodeDeterministicAndRoundTrips(t *testing.T) {
	file := New()
	if err := Upsert(&file, "zeta", Dependency{Constraint: "2.0.0"}); err != nil {
		t.Fatal(err)
	}
	if err := Upsert(&file, "alpha", Dependency{Constraint: "*"}); err != nil {
		t.Fatal(err)
	}
	first, err := Encode(file)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Encode(file)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatalf("serialization is not deterministic:\n%s\n%s", first, second)
	}
	if strings.Index(string(first), `"alpha"`) > strings.Index(string(first), `"zeta"`) {
		t.Fatalf("dependency keys are not sorted: %s", first)
	}
	path := filepath.Join(t.TempDir(), FileName)
	if err := Save(path, file); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	gotBytes, err := Encode(got)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, gotBytes) {
		t.Fatalf("round trip mismatch:\n%s\n%s", first, gotBytes)
	}
}

func TestNormalizeGitHubRepo(t *testing.T) {
	for _, input := range []string{
		"https://github.com/Owner/Repo.git",
		"https://github.com/Owner/Repo.git/",
		"Owner/Repo.git",
	} {
		file := New()
		if err := Upsert(&file, "demo", Dependency{Constraint: "1.0.0", Repo: input}); err != nil {
			t.Fatal(err)
		}
		if got := file.Dependencies["demo"].Repo; got != "Owner/Repo" {
			t.Fatalf("normalized %q = %q", input, got)
		}
	}
}
