package main

import (
	"regexp"
	"testing"
)

// Test Version
func TestVersion(t *testing.T) {
	// Semantic versioning
	// https://semver.org/#is-there-a-suggested-regular-expression-regex-to-check-a-semver-string
	// https://regex101.com/r/vkijKf/1/
	semverRe := regexp.MustCompile(`^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-((?:0|[1-9]\d*|\d*[a-zA-Z-][0-9a-zA-Z-]*)(?:\.(?:0|[1-9]\d*|\d*[a-zA-Z-][0-9a-zA-Z-]*))*))?(?:\+([0-9a-zA-Z-]+(?:\.[0-9a-zA-Z-]+)*))?$`)
	if !semverRe.MatchString(BuildVersion) {
		t.Fatalf("Version not compatible with semantic versioning: %q", BuildVersion)
	}
}
