package publisher

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"strings"

	"github.com/VyxorDev/packrail/internal/registry"
)

type runtimeInference struct {
	Runtime    registry.Runtime
	Entrypoint string
	Args       []string
}

type nativeInference struct {
	Entrypoint string
	Command    string
}

// preferredAssets collapses release variants that map to the same Packrail
// platform. For conventional Linux hosts, glibc/GNU builds are preferred over
// musl builds. Other ties are deterministic by asset name.
func preferredAssets(assets []Asset) ([]Asset, []string) {
	selected := map[string]Asset{}
	var warnings []string
	for _, asset := range assets {
		current, exists := selected[asset.Platform]
		if !exists {
			selected[asset.Platform] = asset
			continue
		}
		winner, loser := current, asset
		if assetPreference(asset) < assetPreference(current) ||
			(assetPreference(asset) == assetPreference(current) && asset.Name < current.Name) {
			winner, loser = asset, current
			selected[asset.Platform] = asset
		}
		warnings = append(warnings, fmt.Sprintf("multiple assets map to %s; selected %s over %s", asset.Platform, winner.Name, loser.Name))
	}
	out := make([]Asset, 0, len(selected))
	for _, asset := range selected {
		out = append(out, asset)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Platform == out[j].Platform {
			return out[i].Name < out[j].Name
		}
		return out[i].Platform < out[j].Platform
	})
	return out, warnings
}

func assetPreference(asset Asset) int {
	name := strings.ToLower(asset.Name)
	if strings.HasPrefix(asset.Platform, "windows-") {
		switch {
		case strings.Contains(name, "msvc"):
			return 0
		case strings.Contains(name, "windows-gnu"):
			return 1
		default:
			return 2
		}
	}
	if strings.HasPrefix(asset.Platform, "linux-") {
		switch {
		case strings.Contains(name, "unknown-linux-gnu"), strings.Contains(name, "linux-gnu"):
			return 0
		case strings.Contains(name, "musl"):
			return 3
		case strings.Contains(name, "static"):
			return 4
		default:
			return 1
		}
	}
	return 0
}

// inferRuntimeEntrypoint is intentionally conservative. It only infers a
// runtime launch when an archive contains a strong command-name match (for
// example tool.py/tool.jar/tool.dll) or exactly one plausible Python/Java
// entrypoint. .NET DLL archives often contain dependency DLLs, so .NET requires
// a command-name match.
func (c *Client) inferRuntimeEntrypoint(asset Asset, command string) (runtimeInference, bool, error) {
	archive, err := c.download(asset)
	if err != nil {
		return runtimeInference{}, false, fmt.Errorf("download for runtime entrypoint detection: %w", err)
	}
	defer os.Remove(archive)

	files, err := archiveFiles(archive, asset.Format)
	if err != nil {
		return runtimeInference{}, false, err
	}
	command = strings.ToLower(strings.TrimSpace(command))

	type candidate struct {
		name string
		ext  string
		rank int
	}
	var candidates []candidate
	for _, name := range files {
		base := strings.ToLower(path.Base(name))
		ext := strings.ToLower(path.Ext(base))
		if ext != ".py" && ext != ".jar" && ext != ".dll" {
			continue
		}
		stem := strings.TrimSuffix(base, ext)
		rank := 10
		if stem == command {
			rank = 0
		} else if ext == ".py" && base == "__main__.py" {
			rank = 2
		}
		candidates = append(candidates, candidate{name: name, ext: ext, rank: rank})
	}
	if len(candidates) == 0 {
		return runtimeInference{}, false, nil
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].rank != candidates[j].rank {
			return candidates[i].rank < candidates[j].rank
		}
		return candidates[i].name < candidates[j].name
	})

	chosen := candidates[0]
	if chosen.rank >= 10 {
		var sameExt []candidate
		for _, candidate := range candidates {
			if candidate.ext == chosen.ext {
				sameExt = append(sameExt, candidate)
			}
		}
		if chosen.ext == ".dll" || len(sameExt) != 1 {
			return runtimeInference{}, false, nil
		}
	}

	switch chosen.ext {
	case ".py":
		return runtimeInference{
			Runtime:    registry.Runtime{Name: "python", Constraint: ">=3.11", Command: "python"},
			Entrypoint: chosen.name,
			Args:       []string{"{entrypoint}"},
		}, true, nil
	case ".jar":
		return runtimeInference{
			Runtime:    registry.Runtime{Name: "java", Constraint: ">=21", Command: "java"},
			Entrypoint: chosen.name,
			Args:       []string{"-jar", "{entrypoint}"},
		}, true, nil
	case ".dll":
		return runtimeInference{
			Runtime:    registry.Runtime{Name: "dotnet", Constraint: ">=8", Command: "dotnet"},
			Entrypoint: chosen.name,
			Args:       []string{"{entrypoint}"},
		}, true, nil
	default:
		return runtimeInference{}, false, nil
	}
}

// inferNativeEntrypoint handles repositories whose repo name differs from the
// command name, such as BurntSushi/ripgrep -> rg. It only accepts one obvious
// native executable: a single .exe in ZIP archives or a single executable-mode
// regular file in tarballs.
func (c *Client) inferNativeEntrypoint(asset Asset) (nativeInference, bool, error) {
	archive, err := c.download(asset)
	if err != nil {
		return nativeInference{}, false, fmt.Errorf("download for native entrypoint detection: %w", err)
	}
	defer os.Remove(archive)

	var candidates []string
	switch asset.Format {
	case "zip":
		reader, err := zip.OpenReader(archive)
		if err != nil {
			return nativeInference{}, false, err
		}
		defer reader.Close()
		for _, file := range reader.File {
			if file.FileInfo().IsDir() {
				continue
			}
			name := strings.ReplaceAll(file.Name, "\\", "/")
			if strings.EqualFold(path.Ext(name), ".exe") {
				candidates = append(candidates, name)
			}
		}
	case "tgz":
		file, err := os.Open(archive)
		if err != nil {
			return nativeInference{}, false, err
		}
		defer file.Close()
		gz, err := gzip.NewReader(file)
		if err != nil {
			return nativeInference{}, false, err
		}
		defer gz.Close()
		tr := tar.NewReader(gz)
		for {
			header, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return nativeInference{}, false, err
			}
			if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA {
				continue
			}
			if header.FileInfo().Mode().Perm()&0o111 == 0 {
				continue
			}
			candidates = append(candidates, strings.ReplaceAll(header.Name, "\\", "/"))
		}
	default:
		return nativeInference{}, false, fmt.Errorf("unsupported archive format %s", asset.Format)
	}

	if len(candidates) != 1 {
		return nativeInference{}, false, nil
	}
	entrypoint := candidates[0]
	command := path.Base(entrypoint)
	command = strings.TrimSuffix(command, path.Ext(command))
	if strings.TrimSpace(command) == "" {
		return nativeInference{}, false, nil
	}
	return nativeInference{Entrypoint: entrypoint, Command: command}, true, nil
}

func archiveFiles(archivePath, format string) ([]string, error) {
	var files []string
	switch format {
	case "zip":
		reader, err := zip.OpenReader(archivePath)
		if err != nil {
			return nil, err
		}
		defer reader.Close()
		for _, file := range reader.File {
			if file.FileInfo().IsDir() {
				continue
			}
			files = append(files, strings.ReplaceAll(file.Name, "\\", "/"))
		}
	case "tgz":
		file, err := os.Open(archivePath)
		if err != nil {
			return nil, err
		}
		defer file.Close()
		gz, err := gzip.NewReader(file)
		if err != nil {
			return nil, err
		}
		defer gz.Close()
		tr := tar.NewReader(gz)
		for {
			header, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, err
			}
			if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA {
				continue
			}
			files = append(files, strings.ReplaceAll(header.Name, "\\", "/"))
		}
	default:
		return nil, fmt.Errorf("unsupported archive format %s", format)
	}
	return files, nil
}
