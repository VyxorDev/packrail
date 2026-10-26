package selfupdate

import "testing"

func TestAssetName(t *testing.T) {
	cases := []struct {
		goos, goarch, want string
	}{
		{"windows", "amd64", "prl-windows-amd64.exe"},
		{"windows", "arm64", "prl-windows-arm64.exe"},
		{"linux", "amd64", "prl-linux-amd64"},
		{"linux", "arm64", "prl-linux-arm64"},
		{"darwin", "amd64", "prl-macos-amd64"},
		{"darwin", "arm64", "prl-macos-arm64"},
	}
	for _, tc := range cases {
		got, err := assetName(tc.goos, tc.goarch)
		if err != nil {
			t.Fatalf("assetName(%s,%s): %v", tc.goos, tc.goarch, err)
		}
		if got != tc.want {
			t.Fatalf("assetName(%s,%s) = %q, want %q", tc.goos, tc.goarch, got, tc.want)
		}
	}
}

func TestChecksumFor(t *testing.T) {
	manifest := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa  prl-linux-amd64\n"
	got, err := checksumFor(manifest, "prl-linux-amd64")
	if err != nil {
		t.Fatal(err)
	}
	if got != "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" {
		t.Fatalf("unexpected checksum %q", got)
	}
}

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"0.6.0-alpha", "0.7.0-alpha", -1},
		{"0.7.0-alpha", "0.7.0-alpha", 0},
		{"0.7.0-alpha", "0.7.0", -1},
		{"0.8.0-alpha", "0.7.0", 1},
	}
	for _, tc := range cases {
		got, err := compareVersions(tc.a, tc.b)
		if err != nil {
			t.Fatalf("compareVersions(%q,%q): %v", tc.a, tc.b, err)
		}
		if got != tc.want {
			t.Fatalf("compareVersions(%q,%q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}
