from pathlib import Path

# Move live clients to the split registry index.
registry = Path('internal/registry/registry.go')
text = registry.read_text()
old = 'defaultRegistryURL = "https://raw.githubusercontent.com/VyxorDev/packrail/main/internal/registry/data/packages.json"'
new = 'defaultRegistryURL = "https://raw.githubusercontent.com/VyxorDev/packrail/main/registry/index.json"'
if old not in text:
    raise SystemExit('default registry URL marker not found')
registry.write_text(text.replace(old, new, 1))

# Use deterministic asset preference for full publisher generation too.
publisher = Path('internal/publisher/publisher.go')
text = publisher.read_text()
needle = '''\tinspection, err := c.Inspect(repoInput)\n\tif err != nil {\n\t\treturn Draft{}, err\n\t}\n\n\tpkg := registry.Package{'''
replacement = '''\tinspection, err := c.Inspect(repoInput)\n\tif err != nil {\n\t\treturn Draft{}, err\n\t}\n\tpreferred, preferenceWarnings := preferredAssets(inspection.Assets)\n\tinspection.Assets = preferred\n\tinspection.Warnings = append(inspection.Warnings, preferenceWarnings...)\n\n\tpkg := registry.Package{'''
if needle not in text:
    raise SystemExit('publisher Generate marker not found')
publisher.write_text(text.replace(needle, replacement, 1))

# Fail submissions early when artifacts are not owned by the declared source repo.
submit_file = Path('internal/submit/github.go')
text = submit_file.read_text()
needle = '''\tif err := registry.ValidatePackage(pkg); err != nil {\n\t\treturn Result{}, fmt.Errorf("validate submission: %w", err)\n\t}\n\towner, repo, err := githubSource(pkg.Source)'''
replacement = '''\tif err := registry.ValidatePackage(pkg); err != nil {\n\t\treturn Result{}, fmt.Errorf("validate submission: %w", err)\n\t}\n\tif err := registry.ValidateGitHubProvenance(pkg); err != nil {\n\t\treturn Result{}, fmt.Errorf("validate submission provenance: %w", err)\n\t}\n\towner, repo, err := githubSource(pkg.Source)'''
if needle not in text:
    raise SystemExit('submission validation marker not found')
submit_file.write_text(text.replace(needle, replacement, 1))

main = Path('cmd/prl/main.go')
text = main.read_text()
if '"github.com/VyxorDev/packrail/internal/submit"' not in text:
    marker = '\t"github.com/VyxorDev/packrail/internal/selfupdate"\n'
    if marker not in text:
        raise SystemExit('main import marker not found')
    text = text.replace(marker, marker + '\t"github.com/VyxorDev/packrail/internal/submit"\n', 1)
text = text.replace('const version = "0.7.0-alpha"', 'const version = "0.8.0-alpha"', 1)

start = text.index('func packageCommand(args []string) error {')
end = text.index('\nfunc runtimeCommand(args []string) error {', start)
package_command = r'''func packageCommand(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: prl package inspect|generate|add|submit <github-repo>")
	}
	client := publisher.New()

	switch args[0] {
	case "inspect":
		if len(args) != 2 {
			return fmt.Errorf("usage: prl package inspect <github-repo>")
		}
		inspection, err := client.Inspect(args[1])
		if err != nil {
			return err
		}
		fmt.Printf("Repository: %s/%s\n", inspection.Owner, inspection.Repo)
		fmt.Printf("Release:    %s\n", inspection.Release)
		fmt.Printf("Version:    %s\n", inspection.Version)
		fmt.Printf("Assets:     %d supported\n", len(inspection.Assets))
		for _, asset := range inspection.Assets {
			fmt.Printf("  %-16s %-4s %s\n", asset.Platform, asset.Format, asset.Name)
		}
		for _, warning := range inspection.Warnings {
			fmt.Fprintf(os.Stderr, "warning: %s\n", warning)
		}
		return nil

	case "generate", "add", "submit":
		repo := args[1]
		command := ""
		legacyRegistryPath := ""
		for i := 2; i < len(args); i++ {
			switch args[i] {
			case "--command":
				if i+1 >= len(args) {
					return fmt.Errorf("--command requires a value")
				}
				command = args[i+1]
				i++
			case "--registry":
				if args[0] != "add" {
					return fmt.Errorf("--registry is only supported by 'prl package add'")
				}
				if i+1 >= len(args) {
					return fmt.Errorf("--registry requires a path")
				}
				legacyRegistryPath = args[i+1]
				i++
			default:
				return fmt.Errorf("unknown package option %q", args[i])
			}
		}

		draft, err := client.Generate(repo, command)
		if err != nil {
			return err
		}
		for _, warning := range draft.Warnings {
			fmt.Fprintf(os.Stderr, "warning: %s\n", warning)
		}
		if args[0] == "generate" {
			data, err := encodePackageJSON(draft.Package)
			if err != nil {
				return fmt.Errorf("encode package draft: %w", err)
			}
			fmt.Print(string(data))
			if !draft.Complete {
				fmt.Fprintln(os.Stderr, "warning: generated draft is incomplete; review command and entrypoints before publishing it")
			}
			return nil
		}
		if !draft.Complete {
			return fmt.Errorf("package draft is incomplete; fix the warnings or provide --command before publishing it")
		}
		if err := registry.ValidateGitHubProvenance(draft.Package); err != nil {
			return err
		}

		if args[0] == "submit" {
			submitter, err := submit.New()
			if err != nil {
				return err
			}
			fmt.Printf("Submitting %s %s to the Packrail registry...\n", draft.Package.Name, draft.Package.Version)
			result, err := submitter.Submit(draft.Package)
			if err != nil {
				return err
			}
			fmt.Printf("Created registry PR #%d: %s\n", result.Number, result.URL)
			fmt.Printf("Branch: %s (%s)\n", result.Branch, result.Repository)
			return nil
		}

		if legacyRegistryPath != "" {
			replaced, err := publisher.UpsertRegistryFile(legacyRegistryPath, draft.Package)
			if err != nil {
				return err
			}
			action := "Added"
			if replaced {
				action = "Updated"
			}
			fmt.Printf("%s %s %s in legacy registry %s\n", action, draft.Package.Name, draft.Package.Version, legacyRegistryPath)
			return nil
		}

		manifestPath := registry.ManifestPath(canonicalManifestDir, draft.Package.Name)
		_, statErr := os.Stat(manifestPath)
		replaced := statErr == nil
		if statErr != nil && !os.IsNotExist(statErr) {
			return statErr
		}
		if replaced {
			oldPkg, err := registry.ReadManifest(manifestPath)
			if err != nil {
				return err
			}
			if err := registry.ValidateMutation(oldPkg, draft.Package); err != nil {
				return err
			}
		}
		writtenPath, err := registry.WriteManifest(canonicalManifestDir, draft.Package)
		if err != nil {
			return err
		}
		count, err := registry.BuildIndex(canonicalManifestDir, canonicalIndexPath)
		if err != nil {
			return err
		}
		action := "Added"
		if replaced {
			action = "Updated"
		}
		fmt.Printf("%s %s %s in %s\n", action, draft.Package.Name, draft.Package.Version, writtenPath)
		fmt.Printf("Rebuilt %s (%d packages).\n", canonicalIndexPath, count)
		fmt.Println("Review the manifest and index diff, run 'prl registry validate --online', then commit.")
		return nil
	default:
		return fmt.Errorf("unknown package command %q; use inspect, generate, add, or submit", args[0])
	}
}
'''
text = text[:start] + package_command + text[end:]

# Add registry authoring commands without disturbing status/sync behavior.
marker = 'func registryCommand(args []string) error {\n'
insert = '''func registryCommand(args []string) error {\n\tif len(args) > 0 && args[0] == "build" {\n\t\treturn registryBuild(args[1:])\n\t}\n\tif len(args) > 0 && args[0] == "validate" {\n\t\treturn registryValidate(args[1:])\n\t}\n'''
if marker not in text:
    raise SystemExit('registryCommand marker not found')
text = text.replace(marker, insert, 1)
text = text.replace(
    'unknown registry command %q; use \'prl registry status\' or \'prl registry sync\'',
    'unknown registry command %q; use status, sync, build, or validate',
    1,
)

# Fix help text and add the v0.8 commands.
text = text.replace(
    '  prl install <package>                         Install a registry package and required runtime\\n  prl install github:owner/repo                 Install directly from a GitHub Release',
    '  prl install <package>                         Install a registry package and required runtime\n  prl install github:owner/repo                 Install directly from a GitHub Release',
)
text = text.replace(
    '  prl package add <github-repo>                 Generate, validate, and upsert registry metadata',
    '  prl package add <github-repo>                 Generate and write a local registry manifest\n  prl package submit <github-repo>              Generate, verify ownership, and open a registry PR',
    1,
)
text = text.replace(
    '  prl registry sync                             Force-sync package metadata from GitHub',
    '  prl registry sync                             Force-sync package metadata from GitHub\n  prl registry build                            Rebuild registry/index.json from package manifests\n  prl registry validate [--online]              Validate manifests, index, provenance, and URLs',
    1,
)
main.write_text(text)

Path('VERSION').write_text('0.8.0-alpha\n')
