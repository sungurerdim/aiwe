// Package engine routes a unit from its stack and its diff: this file decides whether the diff of a
// unit puts it under the security review whose findings the engine must reproduce itself (SPEC §7.3
// step 4, §9.1, DR-15).
package engine

import (
	"path/filepath"
	"strings"

	"github.com/sungurerdim/aiwe/internal/core"
	"github.com/sungurerdim/aiwe/internal/stacks"
)

// NeedsSecurityReview reports whether a unit of the stack profileID needs the security review of
// SPEC §9.1: one of its diff paths has a segment that contains a default security segment, or its
// base name matches one of the stack's security paths (SPEC §8.2, DR-15). A stack the profile table
// does not hold is refused: without its path table the engine cannot decide whether the unit needs
// the review, and absence is not evidence (DR-6, fail closed).
func NeedsSecurityReview(profileID string, paths []string) (bool, error) {
	profile, ok := stacks.ProfileByID(profileID)
	if !ok {
		return false, &core.Error{Outcome: core.OutcomeError,
			Expected: "a stack profile that names its security paths",
			Found:    "the stack " + profileID + " is unknown",
			Next:     "detect the stack of the repository, then resolve the roles of the unit again"}
	}
	for _, path := range paths {
		if securityPath(profile, path) {
			return true, nil
		}
	}
	return false, nil
}

// securityPath reports whether one diff path is in the security table of its stack (SPEC §8.2,
// DR-15): one of its segments contains a default security segment, or its base name matches one of
// the stack's security paths. The segment match is case-blind, and a pattern the engine cannot read
// decides nothing, so such a path takes the review too (fail closed, DR-6).
func securityPath(profile stacks.Profile, path string) bool {
	parts := strings.Split(filepath.ToSlash(path), "/")
	segments := stacks.SecuritySegments()
	for _, part := range parts {
		lower := strings.ToLower(part)
		for _, want := range segments {
			if strings.Contains(lower, want) {
				return true
			}
		}
	}
	for _, pattern := range profile.SecurityPaths {
		matched, err := filepath.Match(pattern, parts[len(parts)-1])
		if err != nil || matched {
			return true
		}
	}
	return false
}
