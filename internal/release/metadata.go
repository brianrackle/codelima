package release

import (
	"fmt"
	"regexp"
	"strings"
)

// Metadata binds a release tag to its packaged version.
type Metadata struct {
	Tag     string
	Version string
}

var releaseTag = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

// ParseTag accepts regular releases. Prerelease suffixes are rejected because
// the Homebrew tap has a single codelima formula.
func ParseTag(tag string) (Metadata, error) {
	if !releaseTag.MatchString(tag) {
		return Metadata{}, fmt.Errorf("release tag must be vMAJOR.MINOR.PATCH: %q", tag)
	}
	return Metadata{Tag: tag, Version: strings.TrimPrefix(tag, "v")}, nil
}
