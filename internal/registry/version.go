package registry

import (
	"fmt"
	"strconv"
	"strings"
)

// ParseDependencyTarget splits a registry dependency request such as
// "ripgrep@15.2.0" or "ripgrep@^15". An omitted constraint means "*".
func ParseDependencyTarget(input string) (name, constraint string, err error) {
	value := strings.TrimSpace(input)
	if value == "" {
		return "", "", fmt.Errorf("dependency target is required")
	}
	if strings.HasPrefix(strings.ToLower(value), "github:") {
		return "", "", fmt.Errorf("GitHub dependency targets are not registry package names")
	}
	name, constraint = value, "*"
	if at := strings.LastIndexByte(value, '@'); at >= 0 {
		name = strings.TrimSpace(value[:at])
		constraint = strings.TrimSpace(value[at+1:])
		if constraint == "" {
			return "", "", fmt.Errorf("dependency %q has an empty version constraint", value)
		}
	}
	name = strings.ToLower(strings.TrimSpace(name))
	if !packageName.MatchString(name) {
		return "", "", fmt.Errorf("invalid package name %q", name)
	}
	if _, err := parseConstraint(constraint); err != nil {
		return "", "", err
	}
	return name, constraint, nil
}

// IsExactConstraint reports whether a supported constraint requests one exact
// version rather than a range or wildcard.
func IsExactConstraint(constraint string) bool {
	constraint = strings.TrimSpace(constraint)
	if constraint == "" || constraint == "*" || strings.HasPrefix(constraint, "^") || strings.HasPrefix(constraint, ">=") {
		return false
	}
	_, err := parseVersion(constraint)
	return err == nil
}

// ConstraintSatisfied checks Packrail's deliberately small dependency
// constraint grammar: *, exact versions, caret ranges, and >= ranges.
func ConstraintSatisfied(version, constraint string) (bool, error) {
	parsed, err := parseConstraint(constraint)
	if err != nil {
		return false, err
	}
	candidate, err := parseVersion(version)
	if err != nil {
		return false, fmt.Errorf("invalid package version %q: %w", version, err)
	}
	switch parsed.kind {
	case constraintAny:
		return true, nil
	case constraintExact:
		return compareParsed(candidate, parsed.version) == 0, nil
	case constraintAtLeast:
		return compareParsed(candidate, parsed.version) >= 0, nil
	case constraintCaret:
		if compareParsed(candidate, parsed.version) < 0 {
			return false, nil
		}
		upper := caretUpperBound(parsed.version)
		return compareParsed(candidate, upper) < 0, nil
	default:
		return false, fmt.Errorf("unsupported constraint %q", constraint)
	}
}

// CompareVersions compares two supported semantic version strings.
func CompareVersions(a, b string) (int, error) {
	left, err := parseVersion(a)
	if err != nil {
		return 0, fmt.Errorf("parse version %q: %w", a, err)
	}
	right, err := parseVersion(b)
	if err != nil {
		return 0, fmt.Errorf("parse version %q: %w", b, err)
	}
	return compareParsed(left, right), nil
}

type constraintKind int

const (
	constraintAny constraintKind = iota
	constraintExact
	constraintCaret
	constraintAtLeast
)

type parsedConstraint struct {
	kind    constraintKind
	version parsedVersion
}

type parsedVersion struct {
	major int
	minor int
	patch int
	pre   []string
}

func parseConstraint(input string) (parsedConstraint, error) {
	value := strings.TrimSpace(input)
	if value == "" {
		return parsedConstraint{}, fmt.Errorf("version constraint is required")
	}
	if value == "*" {
		return parsedConstraint{kind: constraintAny}, nil
	}
	kind := constraintExact
	versionText := value
	switch {
	case strings.HasPrefix(value, ">="):
		kind = constraintAtLeast
		versionText = strings.TrimSpace(value[2:])
	case strings.HasPrefix(value, "^"):
		kind = constraintCaret
		versionText = strings.TrimSpace(value[1:])
	case strings.HasPrefix(value, ">"):
		return parsedConstraint{}, fmt.Errorf("strict > constraints are not supported; use >=")
	case strings.HasPrefix(value, "<"), strings.HasPrefix(value, "~"), strings.HasPrefix(value, "="):
		return parsedConstraint{}, fmt.Errorf("unsupported version constraint %q", value)
	}
	version, err := parseVersion(versionText)
	if err != nil {
		return parsedConstraint{}, fmt.Errorf("invalid version constraint %q: %w", value, err)
	}
	return parsedConstraint{kind: kind, version: version}, nil
}

func parseVersion(input string) (parsedVersion, error) {
	value := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(input), "v"))
	if value == "" {
		return parsedVersion{}, fmt.Errorf("version is empty")
	}
	if strings.Count(value, "+") > 1 {
		return parsedVersion{}, fmt.Errorf("version has multiple build metadata separators")
	}
	if plus := strings.IndexByte(value, '+'); plus >= 0 {
		build := value[plus+1:]
		if build == "" {
			return parsedVersion{}, fmt.Errorf("build metadata is empty")
		}
		if err := validateVersionIdentifiers(build); err != nil {
			return parsedVersion{}, fmt.Errorf("invalid build metadata: %w", err)
		}
		value = value[:plus]
	}
	var pre []string
	if dash := strings.IndexByte(value, '-'); dash >= 0 {
		preText := value[dash+1:]
		value = value[:dash]
		if preText == "" {
			return parsedVersion{}, fmt.Errorf("prerelease identifier is empty")
		}
		if err := validateVersionIdentifiers(preText); err != nil {
			return parsedVersion{}, fmt.Errorf("invalid prerelease: %w", err)
		}
		pre = strings.Split(preText, ".")
	}
	parts := strings.Split(value, ".")
	if len(parts) < 1 || len(parts) > 3 {
		return parsedVersion{}, fmt.Errorf("expected one to three numeric components")
	}
	nums := []int{0, 0, 0}
	for i, part := range parts {
		if part == "" {
			return parsedVersion{}, fmt.Errorf("empty version component")
		}
		n, err := strconv.Atoi(part)
		if err != nil || n < 0 {
			return parsedVersion{}, fmt.Errorf("version component %q is not a non-negative integer", part)
		}
		nums[i] = n
	}
	return parsedVersion{major: nums[0], minor: nums[1], patch: nums[2], pre: pre}, nil
}

func validateVersionIdentifiers(value string) error {
	for _, identifier := range strings.Split(value, ".") {
		if identifier == "" {
			return fmt.Errorf("empty identifier")
		}
		for _, r := range identifier {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' {
				continue
			}
			return fmt.Errorf("identifier %q contains invalid character %q", identifier, r)
		}
	}
	return nil
}

func compareParsed(a, b parsedVersion) int {
	if a.major != b.major {
		if a.major < b.major {
			return -1
		}
		return 1
	}
	if a.minor != b.minor {
		if a.minor < b.minor {
			return -1
		}
		return 1
	}
	if a.patch != b.patch {
		if a.patch < b.patch {
			return -1
		}
		return 1
	}
	if len(a.pre) == 0 && len(b.pre) == 0 {
		return 0
	}
	if len(a.pre) == 0 {
		return 1
	}
	if len(b.pre) == 0 {
		return -1
	}
	max := len(a.pre)
	if len(b.pre) > max {
		max = len(b.pre)
	}
	for i := 0; i < max; i++ {
		if i >= len(a.pre) {
			return -1
		}
		if i >= len(b.pre) {
			return 1
		}
		if a.pre[i] == b.pre[i] {
			continue
		}
		ai, aerr := strconv.Atoi(a.pre[i])
		bi, berr := strconv.Atoi(b.pre[i])
		switch {
		case aerr == nil && berr == nil:
			if ai < bi {
				return -1
			}
			return 1
		case aerr == nil:
			return -1
		case berr == nil:
			return 1
		default:
			if a.pre[i] < b.pre[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}

func caretUpperBound(v parsedVersion) parsedVersion {
	if v.major > 0 {
		return parsedVersion{major: v.major + 1}
	}
	if v.minor > 0 {
		return parsedVersion{minor: v.minor + 1}
	}
	return parsedVersion{patch: v.patch + 1}
}
