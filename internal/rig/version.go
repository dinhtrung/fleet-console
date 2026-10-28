package rig

import (
	"fmt"
	"regexp"
	"strconv"
)

// Floor is the oldest rig this binary speaks to. Captured fixtures live under
// testdata/0.7.0; moving Floor means capturing the new version first.
const Floor = "0.7.0"

// Version is a parsed rig release version.
type Version struct {
	Major int `json:"major"`
	Minor int `json:"minor"`
	Patch int `json:"patch"`
}

var versionPrefix = regexp.MustCompile(`^(\d+)\.(\d+)\.(\d+)`)

// ParseVersion accepts what `rig --version` prints, e.g. "0.7.0 (c99450f3)".
func ParseVersion(raw string) (Version, error) {
	m := versionPrefix.FindStringSubmatch(raw)
	if m == nil {
		return Version{}, fmt.Errorf("cannot parse version %q", raw)
	}
	major, err := strconv.Atoi(m[1])
	if err != nil {
		return Version{}, fmt.Errorf("cannot parse version %q: %w", raw, err)
	}
	minor, err := strconv.Atoi(m[2])
	if err != nil {
		return Version{}, fmt.Errorf("cannot parse version %q: %w", raw, err)
	}
	patch, err := strconv.Atoi(m[3])
	if err != nil {
		return Version{}, fmt.Errorf("cannot parse version %q: %w", raw, err)
	}
	return Version{Major: major, Minor: minor, Patch: patch}, nil
}

// String renders the version as major.minor.patch.
func (v Version) String() string {
	return strconv.Itoa(v.Major) + "." + strconv.Itoa(v.Minor) + "." + strconv.Itoa(v.Patch)
}

// Less reports whether v is older than other.
func (v Version) Less(other Version) bool {
	if v.Major != other.Major {
		return v.Major < other.Major
	}
	if v.Minor != other.Minor {
		return v.Minor < other.Minor
	}
	return v.Patch < other.Patch
}
