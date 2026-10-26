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
