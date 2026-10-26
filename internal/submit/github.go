package submit

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"strings"
	"time"

	"github.com/VyxorDev/packrail/internal/registry"
)

const defaultAPIBase = "https://api.github.com"
const defaultCentralRepo = "VyxorDev/packrail"

var errNotFound = errors.New("github resource not found")

type Client struct {
	HTTP        *http.Client
	APIBase     string
	CentralRepo string
	Token       string
}

type Result struct {
	URL        string
	Number     int
	Branch     string
	Repository string
	Action     string
}

type githubUser struct {
	Login string `json:"login"`
}

type githubRepo struct {
	FullName      string `json:"full_name"`
	DefaultBranch string `json:"default_branch"`
}

type githubRef struct {
	Object struct {
		SHA string `json:"sha"`
	} `json:"object"`
}

type contentFile struct {
	SHA      string `json:"sha"`
	Content  string `json:"content"`
	Encoding string `json:"encoding"`
}

type permissionResponse struct {
	Permission string `json:"permission"`
}

type pullResponse struct {
	HTMLURL string `json:"html_url"`
	Number  int    `json:"number"`
}

func New() (*Client, error) {
	token, err := resolveToken()
	if err != nil {
		return nil, err
	}
	return &Client{
		HTTP:        &http.Client{Timeout: 45 * time.Second},
		APIBase:     defaultAPIBase,
		CentralRepo: defaultCentralRepo,
		Token:       token,
	}, nil
}

func resolveToken() (string, error) {
	for _, name := range []string{"PACKRAIL_GITHUB_TOKEN", "GH_TOKEN", "GITHUB_TOKEN"} {
		if token := strings.TrimSpace(os.Getenv(name)); token != "" {
			return token, nil
		}
	}
	if path, err := exec.LookPath("gh"); err == nil {
		cmd := exec.Command(path, "auth", "token")
		output, err := cmd.Output()
		if err == nil && strings.TrimSpace(string(output)) != "" {
			return strings.TrimSpace(string(output)), nil
		}
	}
	return "", fmt.Errorf("GitHub authentication required: set PACKRAIL_GITHUB_TOKEN (or GH_TOKEN), or authenticate the GitHub CLI with 'gh auth login'")
}

func (c *Client) Submit(pkg registry.Package) (Result, error) {
	if err := registry.ValidatePackage(pkg); err != nil {
		return Result{}, fmt.Errorf("validate submission: %w", err)
	}
	owner, repo, err := githubSource(pkg.Source)
	if err != nil {
		return Result{}, err
	}

	var user githubUser
	if err := c.getJSON("/user", &user); err != nil {
		return Result{}, fmt.Errorf("resolve authenticated GitHub user: %w", err)
	}
	if strings.TrimSpace(user.Login) == "" {
		return Result{}, fmt.Errorf("GitHub returned an empty authenticated user")
	}
	if err := c.verifyPublisher(user.Login, owner, repo); err != nil {
		return Result{}, err
	}

	existing, exists, err := c.centralManifest(pkg.Name)
	if err != nil {
		return Result{}, err
	}
	action := "add"
	if exists {
		action = "update"
		if err := registry.ValidateMutation(existing, pkg); err != nil {
			return Result{}, err
		}
		if existing.Version == pkg.Version {
			return Result{}, fmt.Errorf("package %s %s is already published with identical metadata", pkg.Name, pkg.Version)
		}
	}

	central, err := c.repo(c.CentralRepo)
	if err != nil {
		return Result{}, fmt.Errorf("resolve Packrail registry repository: %w", err)
	}
	centralOwner, centralName, _ := strings.Cut(c.CentralRepo, "/")
	targetRepo := c.CentralRepo
	head := ""
	if !strings.EqualFold(user.Login, centralOwner) {
		targetRepo = user.Login + "/" + centralName
		if err := c.ensureFork(targetRepo, central.DefaultBranch); err != nil {
			return Result{}, err
		}
		head = user.Login + ":"
	}

	baseSHA, err := c.branchSHA(targetRepo, central.DefaultBranch)
	if err != nil {
		return Result{}, fmt.Errorf("resolve submission base branch: %w", err)
	}
	branch := submissionBranch(pkg)
	if err := c.createBranch(targetRepo, branch, baseSHA); err != nil {
		return Result{}, fmt.Errorf("create submission branch: %w", err)
	}

	manifestData, err := registry.EncodePackage(pkg)
	if err != nil {
		return Result{}, err
	}
	manifestPath := path.Join("registry/packages", strings.ToLower(pkg.Name)+".json")
	if err := c.putFile(targetRepo, branch, manifestPath, manifestData, fmt.Sprintf("registry: %s %s %s", action, pkg.Name, pkg.Version)); err != nil {
		return Result{}, fmt.Errorf("write package manifest: %w", err)
	}

	indexPackages, err := c.centralIndex()
	if err != nil {
		return Result{}, err
	}
	indexPackages = upsert(indexPackages, pkg)
	indexData, err := registry.EncodeIndex(indexPackages)
	if err != nil {
		return Result{}, fmt.Errorf("build registry index: %w", err)
	}
	if err := c.putFile(targetRepo, branch, "registry/index.json", indexData, fmt.Sprintf("registry: rebuild index for %s", pkg.Name)); err != nil {
		return Result{}, fmt.Errorf("write registry index: %w", err)
	}

	title := fmt.Sprintf("registry: %s %s %s", action, pkg.Name, pkg.Version)
	body := fmt.Sprintf("Automated Packrail registry submission.\n\n- Package: `%s`\n- Version: `%s`\n- Source: %s\n- Submitted by: @%s\n\nGenerated and validated by `prl package submit`.", pkg.Name, pkg.Version, pkg.Source, user.Login)
	pr, err := c.createPull(title, body, head+branch, central.DefaultBranch)
	if err != nil {
		return Result{}, fmt.Errorf("create registry pull request: %w", err)
	}
	return Result{URL: pr.HTMLURL, Number: pr.Number, Branch: branch, Repository: targetRepo, Action: action}, nil
}

func githubSource(source string) (string, string, error) {
	parsed, err := url.Parse(strings.TrimSpace(source))
	if err != nil {
		return "", "", fmt.Errorf("parse package source: %w", err)
	}
	if !strings.EqualFold(parsed.Host, "github.com") {
		return "", "", fmt.Errorf("registry submission currently requires a github.com source repository")
	}
	parts := strings.Split(strings.Trim(strings.TrimSuffix(parsed.Path, ".git"), "/"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("package source must be a GitHub repository URL such as https://github.com/owner/repo")
	}
	return parts[0], parts[1], nil
}

func (c *Client) verifyPublisher(login, owner, repo string) error {
	var sourceRepo githubRepo
	if err := c.getJSON("/repos/"+url.PathEscape(owner)+"/"+url.PathEscape(repo), &sourceRepo); err != nil {
		return fmt.Errorf("resolve source repository %s/%s: %w", owner, repo, err)
	}
	if strings.EqualFold(login, owner) {
		return nil
	}
	var permission permissionResponse
	endpoint := "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repo) + "/collaborators/" + url.PathEscape(login) + "/permission"
	if err := c.getJSON(endpoint, &permission); err != nil {
		return fmt.Errorf("cannot prove that @%s may publish %s/%s: %w", login, owner, repo, err)
	}
	switch strings.ToLower(permission.Permission) {
	case "admin", "maintain", "push", "write":
		return nil
	default:
		return fmt.Errorf("@%s has %q permission on %s/%s; Packrail submissions require push/maintain/admin access", login, permission.Permission, owner, repo)
	}
}

func (c *Client) centralManifest(name string) (registry.Package, bool, error) {
	file, err := c.getContent(c.CentralRepo, "main", path.Join("registry/packages", strings.ToLower(name)+".json"))
	if errors.Is(err, errNotFound) {
		return registry.Package{}, false, nil
	}
	if err != nil {
		return registry.Package{}, false, fmt.Errorf("read existing registry package: %w", err)
	}
	var pkg registry.Package
	if err := json.Unmarshal(file, &pkg); err != nil {
		return registry.Package{}, false, fmt.Errorf("decode existing registry package: %w", err)
	}
	return pkg, true, nil
}

func (c *Client) centralIndex() ([]registry.Package, error) {
	file, err := c.getContent(c.CentralRepo, "main", "registry/index.json")
	if err != nil {
		return nil, fmt.Errorf("read registry index: %w", err)
	}
	var packages []registry.Package
	if err := json.Unmarshal(file, &packages); err != nil {
		return nil, fmt.Errorf("decode registry index: %w", err)
	}
	if err := registry.ValidatePackages(packages); err != nil {
		return nil, fmt.Errorf("validate current registry index: %w", err)
	}
	return packages, nil
}

func upsert(packages []registry.Package, pkg registry.Package) []registry.Package {
	out := append([]registry.Package(nil), packages...)
	for i := range out {
		if strings.EqualFold(out[i].Name, pkg.Name) {
			out[i] = pkg
			return out
		}
	}
	return append(out, pkg)
}

func submissionBranch(pkg registry.Package) string {
	clean := strings.NewReplacer("/", "-", " ", "-", ":", "-").Replace(pkg.Version)
	return fmt.Sprintf("packrail/%s-%s-%d", strings.ToLower(pkg.Name), clean, time.Now().UTC().Unix())
}

func (c *Client) repo(fullName string) (githubRepo, error) {
	var repo githubRepo
	if err := c.getJSON("/repos/"+fullName, &repo); err != nil {
		return githubRepo{}, err
	}
	return repo, nil
}

func (c *Client) ensureFork(targetRepo, branch string) error {
	if _, err := c.repo(targetRepo); err == nil {
		// Keep an existing fork close to upstream before branching. A 409 means
		// the fork has diverged; fail rather than creating a noisy PR.
		body := map[string]string{"branch": branch}
		var ignored map[string]any
		if err := c.requestJSON(http.MethodPost, "/repos/"+targetRepo+"/merge-upstream", body, &ignored); err != nil {
			return fmt.Errorf("sync existing Packrail fork before submission: %w", err)
		}
		return nil
	} else if !errors.Is(err, errNotFound) {
		return fmt.Errorf("check Packrail fork: %w", err)
	}

	var fork githubRepo
	if err := c.requestJSON(http.MethodPost, "/repos/"+c.CentralRepo+"/forks", map[string]any{}, &fork); err != nil {
		return fmt.Errorf("create Packrail fork: %w", err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := c.repo(targetRepo); err == nil {
			return nil
		}
		time.Sleep(time.Second)
	}
	return fmt.Errorf("GitHub fork creation did not become ready within 30 seconds")
}

func (c *Client) branchSHA(repo, branch string) (string, error) {
	var ref githubRef
	if err := c.getJSON("/repos/"+repo+"/git/ref/heads/"+url.PathEscape(branch), &ref); err != nil {
		return "", err
	}
	if ref.Object.SHA == "" {
		return "", fmt.Errorf("branch %s returned an empty commit SHA", branch)
	}
	return ref.Object.SHA, nil
}

func (c *Client) createBranch(repo, branch, sha string) error {
	payload := map[string]string{"ref": "refs/heads/" + branch, "sha": sha}
	var ref githubRef
	return c.requestJSON(http.MethodPost, "/repos/"+repo+"/git/refs", payload, &ref)
}

func (c *Client) putFile(repo, branch, filePath string, data []byte, message string) error {
	payload := map[string]any{
		"message": message,
		"content": base64.StdEncoding.EncodeToString(data),
		"branch":  branch,
	}
	var current contentFile
	endpoint := "/repos/" + repo + "/contents/" + filePath + "?ref=" + url.QueryEscape(branch)
	if err := c.getJSON(endpoint, &current); err == nil {
		payload["sha"] = current.SHA
	} else if !errors.Is(err, errNotFound) {
		return err
	}
	var response map[string]any
	return c.requestJSON(http.MethodPut, "/repos/"+repo+"/contents/"+filePath, payload, &response)
}

func (c *Client) createPull(title, body, head, base string) (pullResponse, error) {
	payload := map[string]any{"title": title, "body": body, "head": head, "base": base}
	var response pullResponse
	err := c.requestJSON(http.MethodPost, "/repos/"+c.CentralRepo+"/pulls", payload, &response)
	return response, err
}

func (c *Client) getContent(repo, ref, filePath string) ([]byte, error) {
	var response contentFile
	endpoint := "/repos/" + repo + "/contents/" + filePath + "?ref=" + url.QueryEscape(ref)
	if err := c.getJSON(endpoint, &response); err != nil {
		return nil, err
	}
	if response.Encoding != "base64" {
		return nil, fmt.Errorf("GitHub returned unsupported content encoding %q", response.Encoding)
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(response.Content, "\n", ""))
	if err != nil {
		return nil, fmt.Errorf("decode GitHub file content: %w", err)
	}
	return decoded, nil
}

func (c *Client) getJSON(endpoint string, dst any) error {
	return c.requestJSON(http.MethodGet, endpoint, nil, dst)
}

func (c *Client) requestJSON(method, endpoint string, body any, dst any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(data)
	}
	base := strings.TrimRight(c.APIBase, "/")
	req, err := http.NewRequest(method, base+endpoint, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "Packrail/prl")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return errNotFound
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		return fmt.Errorf("GitHub API %s %s: HTTP %s: %s", method, endpoint, resp.Status, strings.TrimSpace(string(data)))
	}
	if dst == nil || resp.StatusCode == http.StatusNoContent {
		return nil
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(dst); err != nil {
		return fmt.Errorf("decode GitHub API response: %w", err)
	}
	return nil
}
