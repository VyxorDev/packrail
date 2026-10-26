package publisher

import (
	"fmt"
	"strings"

	"github.com/VyxorDev/packrail/internal/registry"
)

// GenerateForPlatform builds only the package definition needed for one target
// machine. Direct installs use this path so Packrail never downloads unrelated
// Windows/Linux/macOS archives just to infer an entrypoint.
func (c *Client) GenerateForPlatform(repoInput, commandOverride, goos, goarch string) (Draft, error) {
	inspection, err := c.Inspect(repoInput)
	if err != nil {
		return Draft{}, err
	}

	assets, preferenceWarnings := preferredAssets(inspection.Assets)
	key := registry.PlatformKey(goos, goarch)
	var exact *Asset
	var portable *Asset
	for i := range assets {
		asset := assets[i]
		switch asset.Platform {
		case key:
			copy := asset
			exact = &copy
		case "any":
			copy := asset
			portable = &copy
		}
	}
	if exact != nil {
		inspection.Assets = []Asset{*exact}
	} else if portable != nil {
		inspection.Assets = []Asset{*portable}
	} else {
		return Draft{}, fmt.Errorf("latest release %s has no supported artifact for %s", inspection.Release, key)
	}
	inspection.Warnings = append(inspection.Warnings, preferenceWarnings...)
	return c.generateForDirectInspection(inspection, commandOverride)
}

func (c *Client) generateForDirectInspection(inspection Inspection, commandOverride string) (Draft, error) {
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

	asset := inspection.Assets[0]
	entrypoint := pkg.Command.Entrypoint
	if entrypoint == "" {
		nativeEntrypoint, nativeErr := c.findCommandEntrypoint(asset, pkg.Command.Name)
		if nativeErr == nil {
			entrypoint = nativeEntrypoint
		} else {
			runtimeGuess, runtimeOK, runtimeErr := c.inferRuntimeEntrypoint(asset, pkg.Command.Name)
			if runtimeErr != nil {
				warnings = append(warnings, fmt.Sprintf("%s: runtime inference failed: %v", asset.Name, runtimeErr))
			} else if runtimeOK {
				entrypoint = runtimeGuess.Entrypoint
				pkg.Runtime = runtimeGuess.Runtime
				pkg.Command.Args = append([]string(nil), runtimeGuess.Args...)
			} else {
				nativeGuess, nativeOK, nativeGuessErr := c.inferNativeEntrypoint(asset)
				if nativeGuessErr != nil {
					warnings = append(warnings, fmt.Sprintf("%s: native inference failed: %v", asset.Name, nativeGuessErr))
				} else if nativeOK {
					entrypoint = nativeGuess.Entrypoint
					if strings.TrimSpace(commandOverride) == "" {
						pkg.Command.Name = nativeGuess.Command
					}
				} else {
					warnings = append(warnings, fmt.Sprintf("%s: %v", asset.Name, nativeErr))
				}
			}
		}
	}

	pkg.Artifacts[asset.Platform] = registry.Artifact{
		URL:        asset.URL,
		SHA256:     asset.Digest,
		Format:     asset.Format,
		Entrypoint: entrypoint,
	}
	if entrypoint == "" {
		complete = false
	}
	if pkg.Description == "" {
		pkg.Description = fmt.Sprintf("%s from GitHub Releases.", inspection.Repo)
	}
	if strings.TrimSpace(pkg.Command.Name) == "" {
		complete = false
	}
	return Draft{Package: pkg, Complete: complete, Warnings: warnings}, nil
}
