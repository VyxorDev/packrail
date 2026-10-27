package publisher

import (
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/VyxorDev/packrail/internal/registry"
)

// InspectRelease inspects one explicit GitHub release tag instead of the latest
// release. If a plain semantic version is supplied, Packrail also tries the
// conventional v-prefixed tag.
func (c *Client) InspectRelease(repoInput, tag string) (Inspection, error) {
	owner, repo, err := parseGitHubRepo(repoInput)
	if err != nil {
		return Inspection{}, err
	}
	tag = strings.TrimSpace(tag)
	if tag == "" {
		return Inspection{}, fmt.Errorf("release tag is required")
	}
	var metadata repoResponse
	if err := c.getJSON(fmt.Sprintf("https://api.github.com/repos/%s/%s", owner, repo), &metadata); err != nil {
		return Inspection{}, fmt.Errorf("fetch repository metadata: %w", err)
	}

	candidates := []string{tag}
	if !strings.HasPrefix(tag, "v") {
		candidates = append(candidates, "v"+tag)
	}
	var release releaseResponse
	var lastErr error
	for _, candidate := range candidates {
		endpoint := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/tags/%s", owner, repo, url.PathEscape(candidate))
		if err := c.getJSON(endpoint, &release); err != nil {
			lastErr = err
			continue
		}
		lastErr = nil
		break
	}
	if lastErr != nil {
		return Inspection{}, fmt.Errorf("fetch release %s: %w", tag, lastErr)
	}
	return inspectionFromRelease(metadata, owner, repo, release)
}

// GenerateRelease generates portable package metadata from one explicit GitHub
// release. This gives direct-GitHub dependencies honest historical exact pins
// without pretending Packrail's registry has historical versions.
func (c *Client) GenerateRelease(repoInput, tag, commandOverride string) (Draft, error) {
	inspection, err := c.InspectRelease(repoInput, tag)
	if err != nil {
		return Draft{}, err
	}
	return c.generateInspection(inspection, commandOverride)
}

func inspectionFromRelease(metadata repoResponse, owner, repo string, release releaseResponse) (Inspection, error) {
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
			if platformSpecificAsset(raw.Name) {
				inspection.Warnings = append(inspection.Warnings, fmt.Sprintf("%s targets an unsupported or ambiguous platform and was skipped", raw.Name))
				continue
			}
			platform = "any"
		}
		digest := strings.TrimPrefix(strings.TrimSpace(raw.Digest), "sha256:")
		if len(digest) != 64 {
			inspection.Warnings = append(inspection.Warnings, fmt.Sprintf("%s has no GitHub SHA-256 digest and was skipped", raw.Name))
			continue
		}
		inspection.Assets = append(inspection.Assets, Asset{
			Name: raw.Name, URL: raw.BrowserDownloadURL, Digest: digest,
			Platform: platform, Format: format, Size: raw.Size,
		})
	}
	if len(inspection.Assets) == 0 {
		return Inspection{}, fmt.Errorf("release %s has no supported .zip/.tgz assets with SHA-256 digests", release.TagName)
	}
	sort.Slice(inspection.Assets, func(i, j int) bool {
		if inspection.Assets[i].Platform == inspection.Assets[j].Platform {
			return inspection.Assets[i].Name < inspection.Assets[j].Name
		}
		return inspection.Assets[i].Platform < inspection.Assets[j].Platform
	})
	return inspection, nil
}

func (c *Client) generateInspection(inspection Inspection, commandOverride string) (Draft, error) {
	preferred, preferenceWarnings := preferredAssets(inspection.Assets)
	inspection.Assets = preferred
	inspection.Warnings = append(inspection.Warnings, preferenceWarnings...)

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

	if npmAsset := firstAnyTGZ(inspection.Assets); npmAsset != nil {
		meta, found, metaErr := c.readNPMPackage(*npmAsset)
		if metaErr != nil {
			warnings = append(warnings, fmt.Sprintf("could not inspect npm metadata in %s: %v", npmAsset.Name, metaErr))
		} else if found {
			if strings.TrimSpace(meta.Name) != "" { pkg.Name = normalizePackageName(meta.Name) }
			if strings.TrimSpace(meta.Version) != "" { pkg.Version = meta.Version }
			if strings.TrimSpace(meta.Description) != "" { pkg.Description = meta.Description }
			command, entrypoint := npmCommand(meta)
			if pkg.Command.Name == "" { pkg.Command.Name = command }
			if entrypoint != "" { pkg.Command.Entrypoint = "package/" + strings.TrimPrefix(entrypoint, "./") }
			if strings.TrimSpace(meta.Engines.Node) != "" {
				pkg.Runtime = registry.Runtime{Name: "node", Constraint: meta.Engines.Node, Command: "node"}
			}
		}
	}
	if pkg.Command.Name == "" { pkg.Command.Name = normalizeCommandName(inspection.Repo) }

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
		pkg.Artifacts[asset.Platform] = registry.Artifact{URL: asset.URL, SHA256: asset.Digest, Format: asset.Format, Entrypoint: entrypoint}
		if entrypoint == "" { complete = false }
		seenPlatforms[asset.Platform] = true
	}
	if pkg.Description == "" { pkg.Description = fmt.Sprintf("%s from GitHub Releases.", inspection.Repo) }
	if pkg.Command.Name == "" || len(pkg.Artifacts) == 0 { complete = false }
	return Draft{Package: pkg, Complete: complete, Warnings: warnings}, nil
}
