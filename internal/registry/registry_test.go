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
	pkg, err := Find("ripgrep")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ArtifactFor(pkg, "plan9", "amd64"); err == nil {
		t.Fatal("expected unsupported platform error")
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

	matches, err = Search("ripgrep")
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 || matches[0].Name != "ripgrep" {
		t.Fatalf("unexpected ripgrep matches: %#v", matches)
	}
}
