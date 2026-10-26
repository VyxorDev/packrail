package publisher

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/VyxorDev/packrail/internal/registry"
)

const maxMetadataBytes = 1 << 20

type Client struct {
	HTTP *http.Client
}

type Inspection struct {
	Owner       string
	Repo        string
	Source      string
	Description string
	Release     string
	Version     string
	Assets      []Asset
	Warnings    []string
}

type Asset struct {
	Name       string
	URL        string
	Digest     string
	Platform   string
	Format     string
	Entrypoint string
	Size       int64
}

type Draft struct {
	Package  registry.Package
	Complete bool
	Warnings []string
}

type repoResponse struct {
	Name        string `json:"name"`
	FullName    string `json:"full_name"`
	HTMLURL     string `json:"html_url"`
	Description string `json:"description"`
}

type releaseResponse struct {
	TagName string         `json:"tag_name"`
	Name    string         `json:"name"`
	Assets  []releaseAsset `json:"assets"`
}

type releaseAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Digest             string `json:"digest"`
	Size               int64  `json:"size"`
}

type npmPackageJSON struct {
	Name        string          `json:"name"`
	Version     string          `json:"version"`
	Description string          `json:"description"`
	Bin         json.RawMessage `json:"bin"`
	Engines     struct {
		Node string `json:"node"`
	} `json:"engines"`
}

func New() *Client {
	return &Client{HTTP: &http.Client{Timeout: 2 * time.Minute}}
}

func (c *Client) Inspect(repoInput string) (Inspection, error) {
	owner, repo, err := parseGitHubRepo(repoInput)
	if err != nil {
		return Inspection{}, err
	}

	var metadata repoResponse
	if err := c.getJSON(fmt.Sprintf("https://api.github.com/repos/%s/%s", owner, repo), &metadata); err != nil {
		return Inspection{}, fmt.Errorf("fetch repository metadata: %w", err)
	}
	var release releaseResponse
	if err := c.getJSON(fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest", owner, repo), &release); err != nil {
		return Inspection{}, fmt.Errorf("fetch latest release: %w", err)
	}

	inspection := Inspection{
		Owner:       owner,
		Repo:        repo,
		Source:      metadata.HTMLURL,
		Description: metadata.Description,
		Release:     release.TagName,
		Version:     strings.TrimPrefix(release.TagName, "v"),
	}
	for _, raw := range release.Assets {
		format := archiveFormat(raw.Name)
		if format == "" || isChecksumAsset(raw.Name) {
			continue
		}
		platform := detectPlatform(raw.Name)
		if platform == "" {
			platform = "any"
		}
		digest := strings.TrimPrefix(strings.TrimSpace(raw.Digest), "sha256:")
		if len(digest) != 64 {
			inspection.Warnings = append(inspection.Warnings, fmt.Sprintf("%s has no GitHub SHA-256 digest and was skipped", raw.Name))
			continue
		}
		inspection.Assets = append(inspection.Assets, Asset{
			Name:     raw.Name,
			URL:      raw.BrowserDownloadURL,
			Digest:   digest,
			Platform: platform,
			Format:   format,
			Size:     raw.Size,
		})
	}
	if len(inspection.Assets) == 0 {
		return Inspection{}, fmt.Errorf("latest release %s has no supported .zip/.tgz assets with SHA-256 digests", release.TagName)
	}
	sort.Slice(inspection.Assets, func(i, j int) bool {
		if inspection.Assets[i].Platform == inspection.Assets[j].Platform {
			return inspection.Assets[i].Name < inspection.Assets[j].Name
		}
		return inspection.Assets[i].Platform < inspection.Assets[j].Platform
	})
	return inspection, nil
}

func (c *Client) Generate(repoInput, commandOverride string) (Draft, error) {
	inspection, err := c.Inspect(repoInput)
	if err != nil {
		return Draft{}, err
	}

	pkg := registry.Package{
		Name:        normalizePackageName(inspection.Repo),
		Version:     inspection.Version,
		Description: inspection.Description,
		Source:      inspection.Source,
		Artifacts:   map[string]registry.Artifact{},
		Command:     registry.Command{Name: strings.TrimSpace(commandOverride)},
	}
	warnings := append([]string(nil), inspection.Warnings...)
	complete := true

	// An npm-style tarball can describe almost the whole package itself. This is
	// specifically useful for projects that publish npm pack output to GitHub
	// Releases without publishing to the npm registry.
	if npmAsset := firstAnyTGZ(inspection.Assets); npmAsset != nil {
		meta, found, metaErr := c.readNPMPackage(*npmAsset)
		if metaErr != nil {
			warnings = append(warnings, fmt.Sprintf("could not inspect npm metadata in %s: %v", npmAsset.Name, metaErr))
		} else if found {
			if strings.TrimSpace(meta.Name) != "" {
				pkg.Name = normalizePackageName(meta.Name)
			}
			if strings.TrimSpace(meta.Version) != "" {
				pkg.Version = meta.Version
			}
			if strings.TrimSpace(meta.Description) != "" {
				pkg.Description = meta.Description
			}
			command, entrypoint := npmCommand(meta)
			if pkg.Command.Name == "" {
				pkg.Command.Name = command
			}
			if entrypoint != "" {
				pkg.Command.Entrypoint = "package/" + strings.TrimPrefix(entrypoint, "./")
			}
			if strings.TrimSpace(meta.Engines.Node) != "" {
				pkg.Runtime = registry.Runtime{Name: "node", Constraint: meta.Engines.Node, Command: "node"}
			}
		}
	}

	if pkg.Command.Name == "" {
		pkg.Command.Name = normalizeCommandName(inspection.Repo)
	}

	seenPlatforms := map[string]bool{}
	for _, asset := range inspection.Assets {
		if seenPlatforms[asset.Platform] {
			warnings = append(warnings, fmt.Sprintf("multiple release assets map to %s; skipped %s", asset.Platform, asset.Name))
			complete = false
			continue
		}
		entrypoint := pkg.Command.Entrypoint
		if entrypoint == "" {
			nativeEntrypoint, nativeErr := c.findCommandEntrypoint(asset, pkg.Command.Name)
			if nativeErr == nil {
				entrypoint = nativeEntrypoint
			} else {
				inference, inferred, inferenceErr := c.inferRuntimeEntrypoint(asset, pkg.Command.Name)
				if inferenceErr != nil {
					warnings = append(warnings, fmt.Sprintf("%s: runtime inference failed: %v", asset.Name, inferenceErr))
				} else if inferred {
					entrypoint = inference.Entrypoint
					if pkg.Runtime.Name == "" {
						pkg.Runtime = inference.Runtime
						pkg.Command.Args = append([]string(nil), inference.Args...)
					} else if !strings.EqualFold(pkg.Runtime.Name, inference.Runtime.Name) {
						warnings = append(warnings, fmt.Sprintf("%s inferred runtime %s but package already uses %s", asset.Name, inference.Runtime.Name, pkg.Runtime.Name))
						complete = false
					} else if len(pkg.Command.Args) == 0 {
						pkg.Command.Args = append([]string(nil), inference.Args...)
					}
				} else {
					warnings = append(warnings, fmt.Sprintf("%s: %v", asset.Name, nativeErr))
				}
			}
		}
		artifact := registry.Artifact{
			URL:        asset.URL,
			SHA256:     asset.Digest,
			Format:     asset.Format,
			Entrypoint: entrypoint,
		}
		if entrypoint == "" {
			complete = false
		}
		pkg.Artifacts[asset.Platform] = artifact
		seenPlatforms[asset.Platform] = true
	}
	if pkg.Description == "" {
		pkg.Description = fmt.Sprintf("%s from GitHub Releases.", inspection.Repo)
	}
	if pkg.Command.Name == "" || len(pkg.Artifacts) == 0 {
		complete = false
	}
	return Draft{Package: pkg, Complete: complete, Warnings: warnings}, nil
}

func (c *Client) getJSON(endpoint string, dst any) error {
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "Packrail/prl")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %s", resp.Status)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(dst)
}

func (c *Client) download(asset Asset) (string, error) {
	req, err := http.NewRequest(http.MethodGet, asset.URL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Packrail/prl")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("HTTP %s", resp.Status)
	}
	file, err := os.CreateTemp("", "packrail-inspect-*")
	if err != nil {
		return "", err
	}
	name := file.Name()
	if _, err := io.Copy(file, resp.Body); err != nil {
		file.Close()
		os.Remove(name)
		return "", err
	}
	if err := file.Close(); err != nil {
		os.Remove(name)
		return "", err
	}
	return name, nil
}

func (c *Client) readNPMPackage(asset Asset) (npmPackageJSON, bool, error) {
	archive, err := c.download(asset)
	if err != nil {
		return npmPackageJSON{}, false, err
	}
	defer os.Remove(archive)
	file, err := os.Open(archive)
	if err != nil {
		return npmPackageJSON{}, false, err
	}
	defer file.Close()
	gz, err := gzip.NewReader(file)
	if err != nil {
		return npmPackageJSON{}, false, nil
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			return npmPackageJSON{}, false, nil
		}
		if err != nil {
			return npmPackageJSON{}, false, err
		}
		name := strings.ReplaceAll(header.Name, "\\", "/")
		if name != "package/package.json" || (header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA) {
			continue
		}
		data, err := io.ReadAll(io.LimitReader(tr, maxMetadataBytes))
		if err != nil {
			return npmPackageJSON{}, false, err
		}
		var meta npmPackageJSON
		if err := json.Unmarshal(data, &meta); err != nil {
			return npmPackageJSON{}, false, err
		}
		return meta, true, nil
	}
}

func (c *Client) findCommandEntrypoint(asset Asset, command string) (string, error) {
	archive, err := c.download(asset)
	if err != nil {
		return "", fmt.Errorf("download for entrypoint detection: %w", err)
	}
	defer os.Remove(archive)
	want := strings.ToLower(command)
	wantEXE := want + ".exe"
	var matches []string

	switch asset.Format {
	case "zip":
		reader, err := zip.OpenReader(archive)
		if err != nil {
			return "", err
		}
		defer reader.Close()
		for _, file := range reader.File {
			if file.FileInfo().IsDir() {
				continue
			}
			base := strings.ToLower(path.Base(strings.ReplaceAll(file.Name, "\\", "/")))
			if base == want || base == wantEXE {
				matches = append(matches, strings.ReplaceAll(file.Name, "\\", "/"))
			}
		}
	case "tgz":
		file, err := os.Open(archive)
		if err != nil {
			return "", err
		}
		defer file.Close()
		gz, err := gzip.NewReader(file)
		if err != nil {
			return "", err
		}
		defer gz.Close()
		tr := tar.NewReader(gz)
		for {
			header, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return "", err
			}
			if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA {
				continue
			}
			name := strings.ReplaceAll(header.Name, "\\", "/")
			base := strings.ToLower(path.Base(name))
			if base == want || base == wantEXE {
				matches = append(matches, name)
			}
		}
	default:
		return "", fmt.Errorf("unsupported archive format %s", asset.Format)
	}

	if len(matches) == 1 {
		return matches[0], nil
	}
	if len(matches) == 0 {
		return "", fmt.Errorf("could not infer executable %q; use --command if the repository name differs from the CLI command", command)
	}
	sort.Strings(matches)
	return "", fmt.Errorf("multiple files match command %q: %s", command, strings.Join(matches, ", "))
}

func parseGitHubRepo(input string) (string, string, error) {
	value := strings.TrimSpace(input)
	if value == "" {
		return "", "", fmt.Errorf("GitHub repository is required")
	}
	if !strings.Contains(value, "://") {
		parts := strings.Split(strings.Trim(value, "/"), "/")
		if len(parts) == 2 && parts[0] != "" && parts[1] != "" {
			return parts[0], strings.TrimSuffix(parts[1], ".git"), nil
		}
		return "", "", fmt.Errorf("repository must be owner/repo or https://github.com/owner/repo")
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return "", "", err
	}
	if !strings.EqualFold(parsed.Hostname(), "github.com") {
		return "", "", fmt.Errorf("only github.com repositories are supported")
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("repository URL must contain owner/repo")
	}
	return parts[0], strings.TrimSuffix(parts[1], ".git"), nil
}

func archiveFormat(name string) string {
	lower := strings.ToLower(name)
	switch {
	case strings.HasSuffix(lower, ".tar.gz"), strings.HasSuffix(lower, ".tgz"):
		return "tgz"
	case strings.HasSuffix(lower, ".zip"):
		return "zip"
	default:
		return ""
	}
}

func isChecksumAsset(name string) bool {
	lower := strings.ToLower(name)
	return strings.HasSuffix(lower, ".sha256") || strings.HasSuffix(lower, ".sha256sum") || strings.HasSuffix(lower, ".checksums")
}

func detectPlatform(name string) string {
	lower := strings.ToLower(name)
	osName := ""
	switch {
	case containsAny(lower, "windows", "win32", "win64", "pc-windows", "msvc"):
		osName = "windows"
	case containsAny(lower, "darwin", "macos", "mac-os", "apple-darwin", "osx"):
		osName = "darwin"
	case strings.Contains(lower, "linux"):
		osName = "linux"
	}
	arch := ""
	switch {
	case containsAny(lower, "aarch64", "arm64"):
		arch = "arm64"
	case containsAny(lower, "x86_64", "amd64", "x64"):
		arch = "amd64"
	}
	if osName == "" || arch == "" {
		return ""
	}
	return osName + "-" + arch
}

func containsAny(value string, values ...string) bool {
	for _, candidate := range values {
		if strings.Contains(value, candidate) {
			return true
		}
	}
	return false
}

func firstAnyTGZ(assets []Asset) *Asset {
	for i := range assets {
		if assets[i].Platform == "any" && assets[i].Format == "tgz" {
			return &assets[i]
		}
	}
	return nil
}

func npmCommand(meta npmPackageJSON) (string, string) {
	if len(meta.Bin) == 0 {
		return "", ""
	}
	var single string
	if json.Unmarshal(meta.Bin, &single) == nil && strings.TrimSpace(single) != "" {
		return normalizeCommandName(meta.Name), single
	}
	var commands map[string]string
	if json.Unmarshal(meta.Bin, &commands) == nil {
		if len(commands) == 1 {
			for command, entry := range commands {
				return command, entry
			}
		}
		if entry, ok := commands[normalizeCommandName(meta.Name)]; ok {
			return normalizeCommandName(meta.Name), entry
		}
	}
	return "", ""
}

func normalizePackageName(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	value = strings.TrimPrefix(value, "@")
	value = strings.ReplaceAll(value, "/", "-")
	var b strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	return strings.Trim(b.String(), "-._")
}

func normalizeCommandName(value string) string {
	value = strings.TrimSpace(value)
	if i := strings.LastIndex(value, "/"); i >= 0 {
		value = value[i+1:]
	}
	return strings.TrimPrefix(value, "@")
}

// Keep filepath referenced for platforms where callers pass Windows-like temp
// paths through tests. It also documents that generated entrypoints are archive
// paths, not local OS paths.
var _ = filepath.Separator
