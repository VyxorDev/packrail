package runtimes

import "testing"

func TestMinimumVersion(t *testing.T) {
	tests := []struct {
		input      string
		wantMajor  int
		wantMinor  int
	}{
		{">=20", 20, 0},
		{">=20.0", 20, 0},
		{"24", 24, 0},
		{"v22", 22, 0},
		{">=3.11", 3, 11},
	}
	for _, tc := range tests {
		major, minor, err := minimumVersion(tc.input)
		if err != nil {
			t.Fatalf("minimumVersion(%q): %v", tc.input, err)
		}
		if major != tc.wantMajor || minor != tc.wantMinor {
			t.Fatalf("minimumVersion(%q) = %d.%d, want %d.%d", tc.input, major, minor, tc.wantMajor, tc.wantMinor)
		}
	}
}

func TestSelectNodeVersionPrefersLatestLTS(t *testing.T) {
	releases := []nodeRelease{
		{Version: "v26.12.1", LTS: false},
		{Version: "v24.11.1", LTS: "Krypton"},
		{Version: "v24.10.0", LTS: "Krypton"},
		{Version: "v22.21.0", LTS: "Jod"},
	}
	got, err := selectNodeVersion(releases, 20)
	if err != nil {
		t.Fatal(err)
	}
	if got != "24.11.1" {
		t.Fatalf("selected %s, want 24.11.1", got)
	}
}

func TestSelectNodeVersionRespectsMinimum(t *testing.T) {
	releases := []nodeRelease{
		{Version: "v24.11.1", LTS: "Krypton"},
		{Version: "v22.21.0", LTS: "Jod"},
	}
	got, err := selectNodeVersion(releases, 24)
	if err != nil {
		t.Fatal(err)
	}
	if got != "24.11.1" {
		t.Fatalf("selected %s, want 24.11.1", got)
	}
}

func TestNodeAsset(t *testing.T) {
	cases := []struct {
		os, arch, want, kind string
	}{
		{"windows", "amd64", "node-v24.11.1-win-x64.zip", "zip"},
		{"windows", "arm64", "node-v24.11.1-win-arm64.zip", "zip"},
		{"linux", "amd64", "node-v24.11.1-linux-x64.tar.gz", "tgz"},
		{"darwin", "arm64", "node-v24.11.1-darwin-arm64.tar.gz", "tgz"},
	}
	for _, tc := range cases {
		got, kind, err := nodeAsset("24.11.1", tc.os, tc.arch)
		if err != nil {
			t.Fatalf("nodeAsset(%s/%s): %v", tc.os, tc.arch, err)
		}
		if got != tc.want || kind != tc.kind {
			t.Fatalf("nodeAsset(%s/%s) = %q/%q, want %q/%q", tc.os, tc.arch, got, kind, tc.want, tc.kind)
		}
	}
}

func TestPythonTargets(t *testing.T) {
	cases := []struct{ os, arch, want string }{
		{"windows", "amd64", "x86_64-pc-windows-msvc"},
		{"windows", "arm64", "aarch64-pc-windows-msvc"},
		{"linux", "amd64", "x86_64-unknown-linux-gnu"},
		{"linux", "arm64", "aarch64-unknown-linux-gnu"},
		{"darwin", "amd64", "x86_64-apple-darwin"},
		{"darwin", "arm64", "aarch64-apple-darwin"},
	}
	for _, tc := range cases {
		got, err := pythonTarget(tc.os, tc.arch)
		if err != nil {
			t.Fatalf("pythonTarget(%s/%s): %v", tc.os, tc.arch, err)
		}
		if got != tc.want {
			t.Fatalf("pythonTarget(%s/%s) = %q, want %q", tc.os, tc.arch, got, tc.want)
		}
	}
}

func TestAdoptiumPlatform(t *testing.T) {
	osName, arch, err := adoptiumPlatform("darwin", "arm64")
	if err != nil {
		t.Fatal(err)
	}
	if osName != "mac" || arch != "aarch64" {
		t.Fatalf("got %s/%s", osName, arch)
	}
}

func TestDotnetRID(t *testing.T) {
	cases := map[string]string{
		"windows/amd64": "win-x64",
		"windows/arm64": "win-arm64",
		"linux/amd64":   "linux-x64",
		"linux/arm64":   "linux-arm64",
		"darwin/amd64":  "osx-x64",
		"darwin/arm64":  "osx-arm64",
	}
	for key, want := range cases {
		parts := []rune(key)
		_ = parts
		var osName, arch string
		for i, r := range key {
			if r == '/' {
				osName = key[:i]
				arch = key[i+1:]
				break
			}
		}
		got, err := dotnetRID(osName, arch)
		if err != nil {
			t.Fatalf("dotnetRID(%s): %v", key, err)
		}
		if got != want {
			t.Fatalf("dotnetRID(%s) = %q, want %q", key, got, want)
		}
	}
}
