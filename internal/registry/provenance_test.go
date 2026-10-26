package registry

import (
	"strings"
	"testing"
)

func TestValidateGitHubProvenance(t *testing.T) {
	pkg := testPackage("demo", "1.0.0", "https://github.com/example/demo")
	pkg.Artifacts["any"] = Artifact{
		URL:        "https://github.com/example/demo/releases/download/v1.0.0/demo.zip",
		SHA256:     strings.Repeat("a", 64),
		Format:     "zip",
		Entrypoint: "demo",
	}
	if err := ValidateGitHubProvenance(pkg); err != nil {
		t.Fatal(err)
	}
}

func TestValidateGitHubProvenanceRejectsForeignArtifact(t *testing.T) {
	pkg := testPackage("demo", "1.0.0", "https://github.com/example/demo")
	pkg.Artifacts["any"] = Artifact{
		URL:        "https://github.com/attacker/demo/releases/download/v1.0.0/demo.zip",
		SHA256:     strings.Repeat("a", 64),
		Format:     "zip",
		Entrypoint: "demo",
	}
	if err := ValidateGitHubProvenance(pkg); err == nil || !strings.Contains(err.Error(), "belongs to attacker/demo") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestVerifyPublisherOnlineAcceptsPersonalRepoOwnerWithoutNetwork(t *testing.T) {
	pkg := testPackage("demo", "1.0.0", "https://github.com/example/demo")
	if err := VerifyPublisherOnline(pkg, "Example", ""); err != nil {
		t.Fatal(err)
	}
}
