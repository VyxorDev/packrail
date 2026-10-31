package main

import "testing"

func TestParseGitHubInstallTarget(t *testing.T) {
	tests := []struct {
		name       string
		target     string
		wantRepo   string
		wantDirect bool
		wantErr    bool
	}{
		{name: "registry package", target: "ripgrep"},
		{name: "bare repository", target: "BurntSushi/ripgrep", wantRepo: "BurntSushi/ripgrep", wantDirect: true},
		{name: "explicit repository", target: " github:BurntSushi/ripgrep ", wantRepo: "BurntSushi/ripgrep", wantDirect: true},
		{name: "empty explicit repository", target: "github:", wantDirect: true, wantErr: true},
		{name: "missing repository name", target: "owner/", wantDirect: true, wantErr: true},
		{name: "nested path", target: "owner/repo/sub"},
		{name: "version pin explains project syntax", target: "owner/repo@1.2.3", wantDirect: true, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo, direct, err := parseGitHubInstallTarget(tt.target)
			if direct != tt.wantDirect {
				t.Fatalf("direct = %v, want %v", direct, tt.wantDirect)
			}
			if err != nil && !tt.wantErr {
				t.Fatalf("unexpected error: %v", err)
			}
			if err == nil && tt.wantErr {
				t.Fatal("expected an error")
			}
			if repo != tt.wantRepo {
				t.Fatalf("repo = %q, want %q", repo, tt.wantRepo)
			}
		})
	}
}
