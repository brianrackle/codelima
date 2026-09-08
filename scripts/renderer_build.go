//go:build ignore

// renderer_build validates reviewed patch inputs and emits the Go/native build
// contract. Generated files live only in the installer's private staging area.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/brianrackle/codelima/internal/rendererbuild"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	profile := rendererbuild.DefaultProfile()
	flag.StringVar(&profile.Source, "source", profile.Source, "audited source revision")
	flag.StringVar(&profile.Features, "features", profile.Features, "native feature selection")
	flag.StringVar(&profile.Target, "target", profile.Target, "Zig target")
	flag.StringVar(&profile.CPU, "cpu", profile.CPU, "Zig CPU selection")
	flag.StringVar(&profile.Optimize, "optimize", profile.Optimize, "Zig optimization mode")
	patchDir := flag.String("patches", "scripts/patches", "reviewed patch directory")
	outputDir := flag.String("output", "", "optional generated output directory")
	flag.Parse()
	if profile.Source != rendererbuild.SourceCommit {
		return fmt.Errorf("native source %s is not the reviewed rendererbuild source", profile.Source)
	}
	for _, patch := range []struct{ name, digest string }{
		{"ghostty-vt-codelima.patch", rendererbuild.ModifyKeysPatchSHA256},
		{"ghostty-vt-clipboard-ack.patch", rendererbuild.ClipboardPatchSHA256},
		{"ghostty-vt-graphics-policy.patch", rendererbuild.GraphicsPatchSHA256},
		{"ghostty-vt-snapshot-allocator.patch", rendererbuild.SnapshotAllocatorPatchSHA256},
	} {
		data, err := os.ReadFile(filepath.Join(*patchDir, patch.name))
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != patch.digest {
			return fmt.Errorf("%s checksum differs from reviewed rendererbuild profile", patch.name)
		}
	}
	id := profile.Fingerprint()
	if *outputDir != "" {
		if err := os.MkdirAll(*outputDir, 0o755); err != nil {
			return err
		}
		files := map[string]string{
			".native-build-id":   id + "\n",
			"build-profile.json": string(profile.Manifest()) + "\n",
			"codelima_build.h":   "#ifndef CODELIMA_GHOSTTY_BUILD_H\n#define CODELIMA_GHOSTTY_BUILD_H\n#define CODELIMA_GHOSTTY_BUILD_ID \"" + id + "\"\nconst char *codelima_ghostty_archive_build_identity(void);\n#endif\n",
			"codelima_build.c":   "const char *codelima_ghostty_archive_build_identity(void) { return \"" + id + "\"; }\n",
		}
		for name, contents := range files {
			if err := os.WriteFile(filepath.Join(*outputDir, name), []byte(contents), 0o644); err != nil {
				return err
			}
		}
	}
	fmt.Println(id)
	return nil
}
