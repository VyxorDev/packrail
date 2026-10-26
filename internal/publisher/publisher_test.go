package publisher

import (
	"encoding/json"
	"testing"
)

func TestParseGitHubRepo(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"BurntSushi/ripgrep", "BurntSushi/ripgrep"},
		{"https://github.com/VyxorDev/starterqueue", "VyxorDev/starterqueue"},
		{"https://github.com/VyxorDev/starterqueue.git", "VyxorDev/starterqueue"},
	}
	for _, tc := range cases {
		owner, repo, err := parseGitHubRepo(tc.input)
		if err != nil {
			t.Fatalf("parse %q: %v", tc.input, err)
		}
		if got := owner + "/" + repo; got != tc.want {
			t.Fatalf("parse %q = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestDetectPlatform(t *testing.T) {
	cases := map[string]string{
		"ripgrep-15.2.0-x86_64-pc-windows-msvc.zip":        "windows-amd64",
		"ripgrep-15.2.0-aarch64-pc-windows-msvc.zip":       "windows-arm64",
		"ripgrep-15.2.0-x86_64-unknown-linux-musl.tar.gz":  "linux-amd64",
		"ripgrep-15.2.0-aarch64-unknown-linux-musl.tar.gz": "linux-arm64",
		"ripgrep-15.2.0-x86_64-apple-darwin.tar.gz":        "darwin-amd64",
		"ripgrep-15.2.0-aarch64-apple-darwin.tar.gz":       "darwin-arm64",
		"starterqueue-0.5.0.tgz":                           "",
	}
	for name, want := range cases {
		if got := detectPlatform(name); got != want {
			t.Fatalf("detectPlatform(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestArchiveFormat(t *testing.T) {
	if got := archiveFormat("tool.tar.gz"); got != "tgz" {
		t.Fatalf("tar.gz = %q", got)
	}
	if got := archiveFormat("tool.tgz"); got != "tgz" {
		t.Fatalf("tgz = %q", got)
	}
	if got := archiveFormat("tool.zip"); got != "zip" {
		t.Fatalf("zip = %q", got)
	}
	if got := archiveFormat("tool.exe"); got != "" {
		t.Fatalf("exe = %q", got)
	}
}

func TestNPMCommandMap(t *testing.T) {
	meta := npmPackageJSON{Name: "starterqueue"}
	meta.Bin = json.RawMessage(`{"starterqueue":"dist/src/cli.js"}`)
	command, entry := npmCommand(meta)
	if command != "starterqueue" || entry != "dist/src/cli.js" {
		t.Fatalf("got %q -> %q", command, entry)
	}
}

func TestNPMCommandString(t *testing.T) {
	meta := npmPackageJSON{Name: "@scope/tool"}
	meta.Bin = json.RawMessage(`"bin/tool.js"`)
	command, entry := npmCommand(meta)
	if command != "tool" || entry != "bin/tool.js" {
		t.Fatalf("got %q -> %q", command, entry)
	}
}
