package publisher

import (
	"archive/zip"
	"bytes"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestPreferredAssetsPrefersGNU(t *testing.T) {
	assets := []Asset{
		{Name: "tool-x86_64-unknown-linux-musl.tar.gz", Platform: "linux-amd64"},
		{Name: "tool-x86_64-unknown-linux-gnu.tar.gz", Platform: "linux-amd64"},
	}
	selected, warnings := preferredAssets(assets)
	if len(selected) != 1 {
		t.Fatalf("selected %d assets, want 1", len(selected))
	}
	if selected[0].Name != "tool-x86_64-unknown-linux-gnu.tar.gz" {
		t.Fatalf("selected %q, want GNU asset", selected[0].Name)
	}
	if len(warnings) == 0 {
		t.Fatal("expected duplicate-platform warning")
	}
}

func TestInferRuntimeEntrypoints(t *testing.T) {
	cases := []struct {
		name        string
		filename    string
		command     string
		wantRuntime string
		wantArgs    []string
	}{
		{"python", "bin/tool.py", "tool", "python", []string{"{entrypoint}"}},
		{"java", "lib/tool.jar", "tool", "java", []string{"-jar", "{entrypoint}"}},
		{"dotnet", "app/tool.dll", "tool", "dotnet", []string{"{entrypoint}"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			payload := makeZip(t, map[string][]byte{tc.filename: []byte("payload")})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write(payload)
			}))
			defer server.Close()

			client := New()
			client.HTTP = server.Client()
			got, ok, err := client.inferRuntimeEntrypoint(Asset{URL: server.URL, Format: "zip"}, tc.command)
			if err != nil {
				t.Fatal(err)
			}
			if !ok {
				t.Fatal("expected runtime inference")
			}
			if got.Runtime.Name != tc.wantRuntime {
				t.Fatalf("runtime = %q, want %q", got.Runtime.Name, tc.wantRuntime)
			}
			if got.Entrypoint != tc.filename {
				t.Fatalf("entrypoint = %q, want %q", got.Entrypoint, tc.filename)
			}
			if !reflect.DeepEqual(got.Args, tc.wantArgs) {
				t.Fatalf("args = %#v, want %#v", got.Args, tc.wantArgs)
			}
		})
	}
}

func TestInferNativeEntrypointFromUniqueEXE(t *testing.T) {
	payload := makeZip(t, map[string][]byte{
		"ripgrep/rg.exe":    []byte("binary"),
		"ripgrep/README.md": []byte("docs"),
	})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(payload)
	}))
	defer server.Close()

	client := New()
	client.HTTP = server.Client()
	got, ok, err := client.inferNativeEntrypoint(Asset{URL: server.URL, Format: "zip"})
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected native inference")
	}
	if got.Command != "rg" || got.Entrypoint != "ripgrep/rg.exe" {
		t.Fatalf("unexpected native inference: %#v", got)
	}
}

func makeZip(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for name, data := range files {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}
