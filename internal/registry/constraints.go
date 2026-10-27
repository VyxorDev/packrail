package registry

import (
	"fmt"
	"strings"
)

// FindSatisfied returns the registry's currently published package only when it
// satisfies constraint. The current registry intentionally publishes one
// resolved version per package; this function does not pretend historical
// versions are available when they are not.
func FindSatisfied(name, constraint string) (Package, error) {
	pkg, err := Find(name)
	if err != nil {
		return Package{}, err
	}
	ok, err := ConstraintSatisfied(pkg.Version, constraint)
	if err != nil {
		return Package{}, err
	}
	if !ok {
		return Package{}, fmt.Errorf("package %s publishes %s, which does not satisfy %s; Packrail's registry does not yet retain historical versions", pkg.Name, pkg.Version, strings.TrimSpace(constraint))
	}
	return pkg, nil
}
