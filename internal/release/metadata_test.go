package release

import (
	"strings"
	"testing"
)

func TestParseTag(t *testing.T) {
	for _, tc := range []struct {
		tag, version, formula string
		prerelease            bool
	}{
		{"v0.2.3", "0.2.3", "codelima", false},
		{"v0.3.0-beta.1", "0.3.0-beta.1", "codelima-beta", true},
		{"v1.2.3-beta.10", "1.2.3-beta.10", "codelima-beta", true},
	} {
		t.Run(tc.tag, func(t *testing.T) {
			meta, err := ParseTag(tc.tag)
			if err != nil {
				t.Fatal(err)
			}
			if meta.Tag != tc.tag || meta.Version != tc.version || meta.FormulaName != tc.formula || meta.Prerelease != tc.prerelease {
				t.Fatalf("unexpected metadata: %+v", meta)
			}
		})
	}
	for _, tag := range []string{"", "main", "1.2.3", "v", "v1.2", "v01.2.3", "v1.2.3-beta", "v1.2.3-beta.01", "v1.2.3-rc.1", "v1.2.3+build", "v1.2.3\nformula_name=codelima", "v1.2.3$(false)"} {
		if _, err := ParseTag(tag); err == nil {
			t.Errorf("accepted invalid release tag %q", tag)
		}
	}
}

func TestBetaFormulaIsOptIn(t *testing.T) {
	formula, err := RenderHomebrewFormula(FormulaSpec{
		Repo: "brianrackle/codelima", Tag: "v0.3.0-beta.1",
		Manifests: []Manifest{{Version: "0.3.0-beta.1", GOOS: "darwin", GOARCH: "arm64", AssetName: "codelima_0.3.0-beta.1_darwin_arm64.tar.gz", SHA256: strings.Repeat("a", 64)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`class CodelimaBeta < Formula`, `version "0.3.0-beta.1"`, `keg_only "it provides the opt-in beta channel"`, `/releases/download/v0.3.0-beta.1/codelima_0.3.0-beta.1_darwin_arm64.tar.gz`, `(libexec/"bin").install "#{root}/bin/codelima-renderer-worker"`} {
		if !strings.Contains(formula, want) {
			t.Errorf("formula missing %q:\n%s", want, formula)
		}
	}
}

func TestFormulaRejectsTagVersionMismatch(t *testing.T) {
	for _, tag := range []string{"v0.3.0", "v0.3.0-beta.2", "invalid"} {
		_, err := RenderHomebrewFormula(FormulaSpec{
			Repo: "brianrackle/codelima", Tag: tag,
			Manifests: []Manifest{{Version: "0.3.0-beta.1", GOOS: "linux", GOARCH: "arm64"}},
		})
		if err == nil {
			t.Errorf("accepted mismatched tag %q", tag)
		}
	}
}
