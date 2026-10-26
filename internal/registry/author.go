package registry

import (
	"encoding/json"
	"fmt"
)

// ValidatePackages applies the same validation used for the live and embedded
// registries to an in-memory package set. It is intended for publisher tooling
// before registry metadata is written to disk.
func ValidatePackages(packages []Package) error {
	data, err := json.Marshal(packages)
	if err != nil {
		return fmt.Errorf("encode packages for validation: %w", err)
	}
	if _, err := decodeAndValidate(data); err != nil {
		return err
	}
	return nil
}

// ValidatePackage validates one package definition in isolation.
func ValidatePackage(pkg Package) error {
	return ValidatePackages([]Package{pkg})
}
