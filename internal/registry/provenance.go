package registry

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var verifyHTTPClient = &http.Client{Timeout: 30 * time.Second}

// ValidateGitHubProvenance binds a registry package to one GitHub repository.
// Registry submissions may not point at artifacts owned by a different repo.
func ValidateGitHubProvenance(pkg Package) error {
	owner, repo, err := githubRepoFromURL(pkg.Source)
	if err != nil {
		return fmt.Errorf("package %s source: %w", pkg.Name, err)
	}
	for platform, artifact := range pkg.Artifacts {
		artifactOwner, artifactRepo, err := githubReleaseRepo(artifact.URL)
		if err != nil {
			return fmt.Errorf("package %s artifact %s: %w", pkg.Name, platform, err)
		}
		if !strings.EqualFold(owner, artifactOwner) || !strings.EqualFold(repo, artifactRepo) {
			return fmt.Errorf("package %s artifact %s belongs to %s/%s, but source is %s/%s", pkg.Name, platform, artifactOwner, artifactRepo, owner, repo)
		}
	}
	return nil
}

// VerifyPublisherOnline proves that login controls the package source. Personal
// repositories are bound directly to their owner. Organization repositories may
// be submitted by collaborators with push, maintain, or admin permission.
func VerifyPublisherOnline(pkg Package, login, token string) error {
	owner, repo, err := githubRepoFromURL(pkg.Source)
	if err != nil {
		return err
	}
	login = strings.TrimSpace(login)
	if login == "" {
		return fmt.Errorf("publisher login is required")
	}
	if strings.EqualFold(owner, login) {
		return nil
	}

	endpoint := fmt.Sprintf("https://api.github.com/repos/%s/%s/collaborators/%s/permission", url.PathEscape(owner), url.PathEscape(repo), url.PathEscape(login))
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "Packrail/prl")
	if strings.TrimSpace(token) != "" {
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(token))
	}
	resp, err := verifyHTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<10))
		return fmt.Errorf("cannot prove @%s controls %s/%s: GitHub HTTP %s: %s", login, owner, repo, resp.Status, strings.TrimSpace(string(data)))
	}
	var result struct {
		Permission string `json:"permission"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&result); err != nil {
		return fmt.Errorf("decode GitHub permission response: %w", err)
	}
	switch strings.ToLower(strings.TrimSpace(result.Permission)) {
	case "admin", "maintain", "push", "write":
		return nil
	default:
		return fmt.Errorf("@%s has %q permission on %s/%s; registry submission requires push/maintain/admin access", login, result.Permission, owner, repo)
	}
}

// VerifyPackageOnline checks that the source repository and every declared
// release artifact are currently reachable. Checksums remain the integrity
// authority during installation; this only catches dead or fabricated URLs.
func VerifyPackageOnline(pkg Package) error {
	if err := ValidateGitHubProvenance(pkg); err != nil {
		return err
	}
	if err := verifyURL(pkg.Source); err != nil {
		return fmt.Errorf("source %s: %w", pkg.Source, err)
	}
	for platform, artifact := range pkg.Artifacts {
		if err := verifyURL(artifact.URL); err != nil {
			return fmt.Errorf("artifact %s %s: %w", platform, artifact.URL, err)
		}
	}
	return nil
}

func verifyURL(value string) error {
	req, err := http.NewRequest(http.MethodHead, value, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "Packrail/prl")
	resp, err := verifyHTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 400 {
		return nil
	}
	// Some download endpoints do not implement HEAD consistently. Retry with a
	// one-byte range GET without downloading the full asset.
	if resp.StatusCode == http.StatusMethodNotAllowed || resp.StatusCode == http.StatusForbidden {
		getReq, getErr := http.NewRequest(http.MethodGet, value, nil)
		if getErr != nil {
			return getErr
		}
		getReq.Header.Set("User-Agent", "Packrail/prl")
		getReq.Header.Set("Range", "bytes=0-0")
		getResp, getErr := verifyHTTPClient.Do(getReq)
		if getErr != nil {
			return getErr
		}
		defer getResp.Body.Close()
		if getResp.StatusCode >= 200 && getResp.StatusCode < 400 {
			return nil
		}
		return fmt.Errorf("HTTP %s", getResp.Status)
	}
	return fmt.Errorf("HTTP %s", resp.Status)
}

func githubRepoFromURL(value string) (string, string, error) {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil {
		return "", "", err
	}
	if !strings.EqualFold(parsed.Scheme, "https") || !strings.EqualFold(parsed.Host, "github.com") {
		return "", "", fmt.Errorf("must be an HTTPS github.com repository URL")
	}
	parts := strings.Split(strings.Trim(strings.TrimSuffix(parsed.Path, ".git"), "/"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("must identify exactly one GitHub repository")
	}
	return parts[0], parts[1], nil
}

func githubReleaseRepo(value string) (string, string, error) {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil {
		return "", "", err
	}
	if !strings.EqualFold(parsed.Scheme, "https") || !strings.EqualFold(parsed.Host, "github.com") {
		return "", "", fmt.Errorf("must be an HTTPS github.com release asset URL")
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(parts) < 5 || parts[0] == "" || parts[1] == "" || parts[2] != "releases" || parts[3] != "download" {
		return "", "", fmt.Errorf("must be a GitHub Releases download URL")
	}
	return parts[0], parts[1], nil
}
