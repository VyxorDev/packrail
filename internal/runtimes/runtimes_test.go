package runtimes

import "testing"

func TestMinimumMajor(t *testing.T) {
	tests := map[string]int{
		">=20":   20,
		">=20.0": 20,
		"24":     24,
		"v22":    22,
	}
	for input, want := range tests {
		got, err := minimumMajor(input)
		if err != nil {
			t.Fatalf("minimumMajor(%q): %v", input, err)
		}
		if got != want {
			t.Fatalf("minimumMajor(%q) = %d, want %d", input, got, want)
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
