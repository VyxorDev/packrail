package submit

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/VyxorDev/packrail/internal/registry"
)

func submissionPackage() registry.Package {
	return registry.Package{
		Name:        "demo",
		Version:     "1.0.0",
		Description: "demo",
		Source:      "https://github.com/VyxorDev/demo",
		Artifacts: map[string]registry.Artifact{
			"any": {
				URL:        "https://github.com/VyxorDev/demo/releases/download/v1.0.0/demo.zip",
				SHA256:     strings.Repeat("a", 64),
				Format:     "zip",
				Entrypoint: "demo",
			},
		},
		Command: registry.Command{Name: "demo"},
	}
}

func TestGitHubSource(t *testing.T) {
	owner, repo, err := githubSource("https://github.com/VyxorDev/demo.git")
	if err != nil {
		t.Fatal(err)
	}
	if owner != "VyxorDev" || repo != "demo" {
		t.Fatalf("got %s/%s", owner, repo)
	}
	if _, _, err := githubSource("https://example.com/demo"); err == nil {
		t.Fatal("expected non-GitHub source to fail")
	}
}

func TestSubmitCreatesRegistryPullRequest(t *testing.T) {
	emptyIndex, err := registry.EncodeIndex([]registry.Package{submissionPackageWithName("existing")})
	if err != nil {
		t.Fatal(err)
	}
	var sawManifestPut, sawIndexPut, sawPull bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/user":
			json.NewEncoder(w).Encode(map[string]any{"login": "VyxorDev"})
		case r.Method == http.MethodGet && r.URL.Path == "/repos/VyxorDev/demo":
			json.NewEncoder(w).Encode(map[string]any{"full_name": "VyxorDev/demo", "default_branch": "main"})
		case r.Method == http.MethodGet && r.URL.Path == "/repos/VyxorDev/packrail":
			json.NewEncoder(w).Encode(map[string]any{"full_name": "VyxorDev/packrail", "default_branch": "main"})
		case r.Method == http.MethodGet && r.URL.Path == "/repos/VyxorDev/packrail/contents/registry/packages/demo.json":
			http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
		case r.Method == http.MethodGet && r.URL.Path == "/repos/VyxorDev/packrail/git/ref/heads/main":
			json.NewEncoder(w).Encode(map[string]any{"object": map[string]any{"sha": "abc123"}})
		case r.Method == http.MethodPost && r.URL.Path == "/repos/VyxorDev/packrail/git/refs":
			json.NewEncoder(w).Encode(map[string]any{"object": map[string]any{"sha": "abc123"}})
		case r.Method == http.MethodGet && r.URL.Path == "/repos/VyxorDev/packrail/contents/registry/index.json":
			json.NewEncoder(w).Encode(map[string]any{"sha": "indexsha", "encoding": "base64", "content": base64.StdEncoding.EncodeToString(emptyIndex)})
		case r.Method == http.MethodPut && r.URL.Path == "/repos/VyxorDev/packrail/contents/registry/packages/demo.json":
			sawManifestPut = true
			json.NewEncoder(w).Encode(map[string]any{"content": map[string]any{"sha": "manifest"}})
		case r.Method == http.MethodPut && r.URL.Path == "/repos/VyxorDev/packrail/contents/registry/index.json":
			sawIndexPut = true
			json.NewEncoder(w).Encode(map[string]any{"content": map[string]any{"sha": "index2"}})
		case r.Method == http.MethodPost && r.URL.Path == "/repos/VyxorDev/packrail/pulls":
			sawPull = true
			json.NewEncoder(w).Encode(map[string]any{"html_url": "https://github.com/VyxorDev/packrail/pull/123", "number": 123})
		default:
			t.Fatalf("unexpected request: %s %s?%s", r.Method, r.URL.Path, r.URL.RawQuery)
		}
	}))
	defer server.Close()

	client := &Client{HTTP: server.Client(), APIBase: server.URL, CentralRepo: "VyxorDev/packrail", Token: "test"}
	result, err := client.Submit(submissionPackage())
	if err != nil {
		t.Fatal(err)
	}
	if result.Number != 123 || result.URL == "" {
		t.Fatalf("unexpected result: %+v", result)
	}
	if !sawManifestPut || !sawIndexPut || !sawPull {
		t.Fatalf("missing writes: manifest=%v index=%v pull=%v", sawManifestPut, sawIndexPut, sawPull)
	}
}

func submissionPackageWithName(name string) registry.Package {
	pkg := submissionPackage()
	pkg.Name = name
	pkg.Source = "https://github.com/VyxorDev/" + name
	pkg.Command.Name = name
	pkg.Artifacts["any"] = registry.Artifact{
		URL:        "https://github.com/VyxorDev/" + name + "/releases/download/v1.0.0/" + name + ".zip",
		SHA256:     strings.Repeat("b", 64),
		Format:     "zip",
		Entrypoint: name,
	}
	return pkg
}
