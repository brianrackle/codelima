package release

import (
	"fmt"
	"regexp"
	"strings"
)

// Metadata binds the GitHub release channel and Homebrew formula to one tag.
type Metadata struct {
	Tag         string
	Version     string
	FormulaName string
	Prerelease  bool
}

var releaseTag = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-beta\.(0|[1-9][0-9]*))?$`)

// ParseTag accepts stable and numbered beta releases. Reject other suffixes so
// an unrecognized prerelease can never silently update the stable formula.
func ParseTag(tag string) (Metadata, error) {
	if !releaseTag.MatchString(tag) {
		return Metadata{}, fmt.Errorf("release tag must be vMAJOR.MINOR.PATCH or vMAJOR.MINOR.PATCH-beta.N: %q", tag)
	}
	meta := Metadata{Tag: tag, Version: strings.TrimPrefix(tag, "v"), FormulaName: "codelima"}
	if strings.Contains(tag, "-beta.") {
		meta.Prerelease = true
		meta.FormulaName = "codelima-beta"
	}
	return meta, nil
}
