package lockfile

import (
	"encoding/json"
	"fmt"
)

// Validate checks a prospective lockfile without writing it or changing the
// caller's value. Project transactions use this before installation/commit.
func Validate(file File) error {
	data, err := json.Marshal(file)
	if err != nil {
		return fmt.Errorf("clone lockfile for validation: %w", err)
	}
	var clone File
	if err := json.Unmarshal(data, &clone); err != nil {
		return fmt.Errorf("clone lockfile for validation: %w", err)
	}
	return normalizeAndValidate(&clone)
}
