package registry

import "testing"

func TestFindStarterQueue(t *testing.T) {
	pkg, err := Find("starterqueue")
	if err != nil {
		t.Fatal(err)
	}
	if pkg.Version != "0.5.0" {
		t.Fatalf("version = %q, want 0.5.0", pkg.Version)
	}
	if pkg.Command.Name != "starterqueue" {
		t.Fatalf("command = %q, want starterqueue", pkg.Command.Name)
	}
	if pkg.Artifact.SHA256 == "" {
		t.Fatal("expected SHA-256 to be mandatory")
	}
}

func TestSearch(t *testing.T) {
	matches, err := Search("queue")
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 || matches[0].Name != "starterqueue" {
		t.Fatalf("unexpected matches: %#v", matches)
	}
}
