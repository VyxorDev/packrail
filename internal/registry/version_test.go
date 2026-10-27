package registry

import "testing"

func TestConstraintSatisfied(t *testing.T) {
	tests := []struct {
		version    string
		constraint string
		want       bool
	}{
		{"15.2.0", "*", true},
		{"15.2.0", "15.2.0", true},
		{"15.2.1", "15.2.0", false},
		{"15.9.0", "^15.2.0", true},
		{"16.0.0", "^15.2.0", false},
		{"0.5.9", "^0.5.0", true},
		{"0.6.0", "^0.5.0", false},
		{"0.0.4", "^0.0.3", false},
		{"15.2.0", ">=15", true},
		{"14.9.9", ">=15", false},
	}
	for _, tt := range tests {
		t.Run(tt.version+"_"+tt.constraint, func(t *testing.T) {
			got, err := ConstraintSatisfied(tt.version, tt.constraint)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("ConstraintSatisfied(%q, %q) = %v, want %v", tt.version, tt.constraint, got, tt.want)
			}
		})
	}
}

func TestConstraintRejectsUnsupportedOrMalformedGrammar(t *testing.T) {
	for _, constraint := range []string{
		">15", "<16", "~15", "=15", "banana",
		"1.0.0-alpha..1", "1.0.0+", "1.0.0+meta..x",
	} {
		if _, err := ConstraintSatisfied("15.2.0", constraint); err == nil {
			t.Fatalf("expected %q to be rejected", constraint)
		}
	}
}

func TestParseDependencyTarget(t *testing.T) {
	name, constraint, err := ParseDependencyTarget("Ripgrep@^15")
	if err != nil {
		t.Fatal(err)
	}
	if name != "ripgrep" || constraint != "^15" {
		t.Fatalf("got %q %q", name, constraint)
	}
	name, constraint, err = ParseDependencyTarget("starterqueue")
	if err != nil {
		t.Fatal(err)
	}
	if name != "starterqueue" || constraint != "*" {
		t.Fatalf("got %q %q", name, constraint)
	}
}

func TestCompareVersions(t *testing.T) {
	if got, err := CompareVersions("1.2.3", "1.2.2"); err != nil || got <= 0 {
		t.Fatalf("expected 1.2.3 > 1.2.2, got %d err=%v", got, err)
	}
	if got, err := CompareVersions("1.2.3-alpha.1", "1.2.3"); err != nil || got >= 0 {
		t.Fatalf("expected prerelease < release, got %d err=%v", got, err)
	}
}
