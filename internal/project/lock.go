package project

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

var errProjectBusy = errors.New("another Packrail process is already operating in this project")

func (m *Manager) acquireOperationLock() (func() error, error) {
	cacheDir, err := os.UserCacheDir()
	if err != nil || strings.TrimSpace(cacheDir) == "" {
		cacheDir = os.TempDir()
	}
	projectPath := filepath.Clean(m.Dir)
	if runtime.GOOS == "windows" {
		projectPath = strings.ToLower(projectPath)
	}
	digest := sha256.Sum256([]byte(projectPath))
	path := filepath.Join(cacheDir, "packrail", "project-locks", hex.EncodeToString(digest[:])+".lock")
	release, err := acquireProjectLock(path)
	if err != nil {
		return nil, fmt.Errorf("lock project %s: %w", m.Dir, err)
	}
	return release, nil
}
