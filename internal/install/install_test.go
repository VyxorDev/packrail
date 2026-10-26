package install

import (
	"archive/tar"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

func writeTGZ(t *testing.T, entries map[string]string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.tgz")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(file)
	tw := tar.NewWriter(gz)
	for name, contents := range entries {
		header := &tar.Header{Name: name, Mode: 0o644, Size: int64(len(contents)), Typeflag: tar.TypeReg}
		if err := tw.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(contents)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestExtractTGZ(t *testing.T) {
	archive := writeTGZ(t, map[string]string{"package/bin/tool.js": "console.log('ok')"})
	destination := t.TempDir()
	if err := extractTGZ(archive, destination); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(destination, "package", "bin", "tool.js"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "console.log('ok')" {
		t.Fatalf("unexpected extracted content: %q", data)
	}
}

func TestExtractTGZRejectsTraversal(t *testing.T) {
	archive := writeTGZ(t, map[string]string{"../escape.txt": "nope"})
	if err := extractTGZ(archive, t.TempDir()); err == nil {
		t.Fatal("expected path traversal archive to be rejected")
	}
}
