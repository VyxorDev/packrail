package runtimes

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/VyxorDev/packrail/internal/registry"
)

// LockedVersion returns the exact runtime version configured by the active v2
// lockfile for the current platform. The boolean is false for one-off installs
// and v1 lockfiles, which intentionally retain constraint-based resolution.
func LockedVersion(spec registry.Runtime) (string, bool, error) {
	locked, pin, pinned, err := lockedPinForCurrentPlatform(spec)
	if err != nil || !pinned {
		return "", pinned, err
	}
	_ = locked
	return pin.Version, true, nil
}

// LockedCurrent verifies that the runtime already stored under PACKRAIL_HOME
// matches the complete v2 pin identity for the current platform: version, URL,
// checksum, format, and a usable pinned executable. Comparing only a version
// string is insufficient because providers can republish metadata for the same
// semantic version or a lockfile can intentionally select a different artifact.
func LockedCurrent(home string, spec registry.Runtime) (bool, bool, error) {
	locked, pin, pinned, err := lockedPinForCurrentPlatform(spec)
	if err != nil || !pinned {
		return false, pinned, err
	}
	platform := registry.PlatformKey(runtime.GOOS, runtime.GOARCH)
	targetDir := filepath.Join(home, "runtimes", canonicalRuntimeName(locked.Name), pin.Version, platform)
	markerPath := filepath.Join(targetDir, ".packrail-runtime.json")
	data, err := os.ReadFile(markerPath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, true, nil
		}
		return false, true, fmt.Errorf("read locked runtime marker: %w", err)
	}
	var marker runtimeMarker
	if err := json.Unmarshal(data, &marker); err != nil {
		return false, true, nil
	}
	if marker.Version != pin.Version ||
		marker.URL != pin.URL ||
		!strings.EqualFold(marker.SHA256, pin.SHA256) ||
		marker.Format != pin.Format {
		return false, true, nil
	}
	if _, err := findRuntimeExecutable(targetDir, pin.Executables); err != nil {
		return false, true, nil
	}
	return true, true, nil
}

func lockedPinForCurrentPlatform(spec registry.Runtime) (LockedRuntime, Pin, bool, error) {
	configuredRuntimePins.RLock()
	locked, ok := configuredRuntimePins.items[SpecKey(spec)]
	configuredRuntimePins.RUnlock()
	if !ok {
		return LockedRuntime{}, Pin{}, false, nil
	}
	platform := registry.PlatformKey(runtime.GOOS, runtime.GOARCH)
	pin, ok := locked.Artifacts[platform]
	if !ok {
		return LockedRuntime{}, Pin{}, true, fmt.Errorf("locked %s runtime has no artifact for %s", locked.Name, platform)
	}
	if err := validatePin(pin); err != nil {
		return LockedRuntime{}, Pin{}, true, fmt.Errorf("invalid locked %s runtime for %s: %w", locked.Name, platform, err)
	}
	return locked, pin, true, nil
}
