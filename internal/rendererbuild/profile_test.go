package rendererbuild

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestIdentityMatchesDefaultProfile(t *testing.T) {
	if identityOverride != "" {
		t.Skip("build selected an explicit native profile")
	}
	if got := Identity(); got != DefaultProfile().Fingerprint() || len(got) != 64 {
		t.Fatalf("identity = %q", got)
	}
}

func TestReviewedPatchDigestsMatchInputs(t *testing.T) {
	for name, expected := range map[string]string{
		"ghostty-vt-codelima.patch":           ModifyKeysPatchSHA256,
		"ghostty-vt-clipboard-ack.patch":      ClipboardPatchSHA256,
		"ghostty-vt-graphics-policy.patch":    GraphicsPatchSHA256,
		"ghostty-vt-snapshot-allocator.patch": SnapshotAllocatorPatchSHA256,
	} {
		data, err := os.ReadFile(filepath.Join("..", "..", "scripts", "patches", name))
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(data)
		if got := hex.EncodeToString(sum[:]); got != expected {
			t.Errorf("%s digest %s differs from profile %s", name, got, expected)
		}
	}
}

func TestEveryProfileInputAffectsIdentity(t *testing.T) {
	base := DefaultProfile()
	changes := map[string]func(*Profile){
		"schema":                   func(p *Profile) { p.Schema++ },
		"source":                   func(p *Profile) { p.Source += "x" },
		"compiler":                 func(p *Profile) { p.Compiler += "x" },
		"features":                 func(p *Profile) { p.Features += ",+graphics" },
		"modify_keys_patch":        func(p *Profile) { p.ModifyKeysPatch += "x" },
		"clipboard_patch":          func(p *Profile) { p.ClipboardPatch += "x" },
		"graphics_patch":           func(p *Profile) { p.GraphicsPatch += "x" },
		"snapshot_allocator_patch": func(p *Profile) { p.SnapshotAllocatorPatch += "x" },
		"width_policy":             func(p *Profile) { p.WidthPolicy += "x" },
		"os":                       func(p *Profile) { p.OS += "x" },
		"arch":                     func(p *Profile) { p.Arch += "x" },
		"target":                   func(p *Profile) { p.Target += "x" },
		"cpu":                      func(p *Profile) { p.CPU += "x" },
		"optimize":                 func(p *Profile) { p.Optimize += "x" },
		"artifact":                 func(p *Profile) { p.Artifact += "x" },
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			modified := base
			change(&modified)
			if modified.Fingerprint() == base.Fingerprint() {
				t.Fatal("changed native input retained the same identity")
			}
		})
	}
}
