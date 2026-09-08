package release

import (
	"strings"
	"testing"
)

func TestParseTag(t *testing.T) {
	for _, tc := range []struct {
		tag, version string
	}{
		{"v0.2.3", "0.2.3"},
		{"v0.3.0", "0.3.0"},
		{"v1.2.3", "1.2.3"},
	} {
		t.Run(tc.tag, func(t *testing.T) {
			meta, err := ParseTag(tc.tag)
			if err != nil {
				t.Fatal(err)
			}
			if meta.Tag != tc.tag || meta.Version != tc.version {
				t.Fatalf("unexpected metadata: %+v", meta)
			}
		})
	}
	for _, tag := range []string{"", "main", "1.2.3", "v", "v1.2", "v01.2.3", "v1.2.3-beta", "v1.2.3-beta.1", "v0.3.0-beta.3", "v1.2.3-beta.01", "v1.2.3-rc.1", "v1.2.3+build", "v1.2.3\nformula_name=codelima", "v1.2.3$(false)"} {
		if _, err := ParseTag(tag); err == nil {
			t.Errorf("accepted invalid release tag %q", tag)
		}
	}
}

func TestFormulaRejectsRetiredBetaChannel(t *testing.T) {
	_, err := RenderHomebrewFormula(FormulaSpec{
		Repo: "brianrackle/codelima", Tag: "v0.3.0-beta.3",
		Manifests: []Manifest{{Version: "0.3.0-beta.3", GOOS: "darwin", GOARCH: "arm64", AssetName: "codelima_0.3.0-beta.3_darwin_arm64.tar.gz", SHA256: strings.Repeat("a", 64)}},
	})
	if err == nil {
		t.Fatal("rendered a formula for the retired beta channel")
	}
}

func TestFormulaRejectsTagVersionMismatch(t *testing.T) {
	for _, tag := range []string{"v0.3.1", "v0.3.0-beta.3", "invalid"} {
		_, err := RenderHomebrewFormula(FormulaSpec{
			Repo: "brianrackle/codelima", Tag: tag,
			Manifests: []Manifest{{Version: "0.3.0", GOOS: "linux", GOARCH: "arm64"}},
		})
		if err == nil {
			t.Errorf("accepted mismatched tag %q", tag)
		}
	}
}
