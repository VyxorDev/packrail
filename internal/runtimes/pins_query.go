package runtimes

import (
	"fmt"
	"runtime"

	"github.com/VyxorDev/packrail/internal/registry"
)

// LockedVersion returns the exact runtime version configured by the active v2
// lockfile for the current platform. The boolean is false for one-off installs
// and v1 lockfiles, which intentionally retain constraint-based resolution.
func LockedVersion(spec registry.Runtime) (string, bool, error) {
	configuredRuntimePins.RLock()
	locked, ok := configuredRuntimePins.items[SpecKey(spec)]
	configuredRuntimePins.RUnlock()
	if !ok {
		return "", false, nil
	}
	platform := registry.PlatformKey(runtime.GOOS, runtime.GOARCH)
	pin, ok := locked.Artifacts[platform]
	if !ok {
		return "", true, fmt.Errorf("locked %s runtime has no artifact for %s", locked.Name, platform)
	}
	if err := validatePin(pin); err != nil {
		return "", true, fmt.Errorf("invalid locked %s runtime for %s: %w", locked.Name, platform, err)
	}
	return pin.Version, true, nil
}
