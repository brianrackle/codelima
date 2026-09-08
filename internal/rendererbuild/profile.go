// Package rendererbuild defines the audited native renderer build contract.
// It deliberately has no cgo dependency, so the supervisor can reject a worker
// built against a different native dependency before sending it terminal data.
package rendererbuild

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"runtime"
)

const (
	SourceCommit                 = "82232ecde55405559dec29c5466cb9e39938cb41"
	ZigVersion                   = "0.16.0"
	Features                     = "-all,+formatter,+selection,+render-state,+input-encode,+color,+grid-introspection,+snapshot,+search,+kitty-graphics"
	ModifyKeysPatchSHA256        = "70647221ebe0376e666c3f28d25b491bd6e3d20ff277bd2bd253f9038c2532e8"
	ClipboardPatchSHA256         = "a23975be68fe431e2dff016b662799be7f989accce6f025e6ba7363ba7acf42b"
	GraphicsPatchSHA256          = "f4b571cb2581c2ae6d8e1e2614ed3b44cb072a35c4fd2c31cc3d1732f339c0f0"
	SnapshotAllocatorPatchSHA256 = "3054c43a2db979c2fbd6445cfb53f9821e7a3fdd00c793ea12312f82b7c0a530"
	WidthPolicy                  = "ghostty-unicode-table-default"
)

// Profile contains all selected native compiler and semantic inputs. Defaults
// not selectable here (including the Unicode table and minimum target OS) are
// fixed by SourceCommit. Schema changes invalidate checkpoint compatibility.
type Profile struct {
	Schema                 int    `json:"schema"`
	Source                 string `json:"source"`
	Compiler               string `json:"compiler"`
	Features               string `json:"features"`
	ModifyKeysPatch        string `json:"modify_keys_patch"`
	ClipboardPatch         string `json:"clipboard_patch"`
	GraphicsPatch          string `json:"graphics_patch"`
	SnapshotAllocatorPatch string `json:"snapshot_allocator_patch"`
	WidthPolicy            string `json:"width_policy"`
	OS                     string `json:"os"`
	Arch                   string `json:"arch"`
	Target                 string `json:"target"`
	CPU                    string `json:"cpu"`
	Optimize               string `json:"optimize"`
	Artifact               string `json:"artifact"`
}

func DefaultProfile() Profile {
	return Profile{
		Schema: 1, Source: SourceCommit, Compiler: "zig-" + ZigVersion,
		Features: Features, ModifyKeysPatch: ModifyKeysPatchSHA256,
		ClipboardPatch: ClipboardPatchSHA256, GraphicsPatch: GraphicsPatchSHA256, WidthPolicy: WidthPolicy,
		SnapshotAllocatorPatch: SnapshotAllocatorPatchSHA256,
		OS:                     runtime.GOOS, Arch: runtime.GOARCH, Target: "native", CPU: "baseline",
		Optimize: "ReleaseSmall", Artifact: "static",
	}
}

// Manifest returns the canonical, ordered JSON input to Fingerprint.
func (p Profile) Manifest() []byte {
	data, _ := json.Marshal(p) // A struct of scalars cannot fail JSON encoding.
	return data
}

func (p Profile) Fingerprint() string {
	sum := sha256.Sum256(p.Manifest())
	return hex.EncodeToString(sum[:])
}

// identityOverride is populated by make for an explicitly selected non-default
// native profile. The default keeps ordinary go test/build deterministic and
// never reads mutable installed files at runtime.
var identityOverride string

func Identity() string {
	if identityOverride != "" {
		return identityOverride
	}
	return DefaultProfile().Fingerprint()
}
