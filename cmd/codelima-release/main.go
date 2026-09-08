package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/brianrackle/codelima/internal/release"
)

type stringSlice []string

func (s *stringSlice) String() string {
	return fmt.Sprintf("%v", []string(*s))
}

func (s *stringSlice) Set(value string) error {
	*s = append(*s, value)
	return nil
}

func main() {
	if len(os.Args) < 2 {
		fatalf("usage: codelima-release <archive|formula|metadata> [flags]")
	}

	switch os.Args[1] {
	case "archive":
		runArchive(os.Args[2:])
	case "formula":
		runFormula(os.Args[2:])
	case "metadata":
		runMetadata(os.Args[2:])
	default:
		fatalf("unknown command %q", os.Args[1])
	}
}

func runMetadata(args []string) {
	fs := flag.NewFlagSet("metadata", flag.ExitOnError)
	tag := fs.String("tag", "", "release tag in vMAJOR.MINOR.PATCH form")
	if err := fs.Parse(args); err != nil {
		fatalf("parse metadata flags: %v", err)
	}
	meta, err := release.ParseTag(*tag)
	if err != nil {
		fatalf("%v", err)
	}
	fmt.Printf("tag=%s\nversion=%s\n", meta.Tag, meta.Version)
}

func runArchive(args []string) {
	fs := flag.NewFlagSet("archive", flag.ExitOnError)
	var (
		version   = fs.String("version", "", "release version without the tag prefix")
		goos      = fs.String("goos", "", "target operating system")
		goarch    = fs.String("goarch", "", "target architecture")
		binary    = fs.String("binary", "", "path to the compiled codelima binary")
		renderer  = fs.String("renderer-binary", "", "path to the compiled renderer worker binary")
		buildID   = fs.String("renderer-build-id", "", "exact native renderer build fingerprint embedded in both binaries")
		outputDir = fs.String("output-dir", "", "directory where release artifacts will be written")
	)
	if err := fs.Parse(args); err != nil {
		fatalf("parse archive flags: %v", err)
	}

	assetName, err := release.ArchiveName(*version, *goos, *goarch)
	if err != nil {
		fatalf("%v", err)
	}
	outputDirValue := *outputDir
	if outputDirValue == "" {
		fatalf("output-dir is required")
	}
	outputPath := filepath.Join(outputDirValue, assetName)
	manifest, err := release.BuildArchive(*version, *goos, *goarch, *binary, *renderer, *buildID, outputPath)
	if err != nil {
		fatalf("build archive: %v", err)
	}
	manifestPath := outputPath + ".json"
	if err := release.WriteManifest(manifestPath, manifest); err != nil {
		fatalf("write manifest: %v", err)
	}
	fmt.Println(outputPath)
	fmt.Println(manifestPath)
}

func runFormula(args []string) {
	fs := flag.NewFlagSet("formula", flag.ExitOnError)
	var (
		repo      = fs.String("repo", "", "GitHub repository owner/name")
		tag       = fs.String("tag", "", "Git tag for the release, such as v1.2.3")
		output    = fs.String("output", "", "path to the rendered Homebrew formula")
		manifests stringSlice
	)
	fs.Var(&manifests, "manifest", "path to a release manifest file")
	if err := fs.Parse(args); err != nil {
		fatalf("parse formula flags: %v", err)
	}

	if *output == "" {
		fatalf("output is required")
	}

	manifestPaths := release.SortedManifestPaths(manifests)
	loaded := make([]release.Manifest, 0, len(manifestPaths))
	for _, manifestPath := range manifestPaths {
		manifest, err := release.ReadManifest(manifestPath)
		if err != nil {
			fatalf("read manifest %s: %v", manifestPath, err)
		}
		loaded = append(loaded, manifest)
	}

	formula, err := release.RenderHomebrewFormula(release.FormulaSpec{
		Repo:      *repo,
		Tag:       *tag,
		Manifests: loaded,
	})
	if err != nil {
		fatalf("render formula: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(*output), 0o755); err != nil {
		fatalf("create output directory: %v", err)
	}
	if err := os.WriteFile(*output, []byte(formula), 0o644); err != nil {
		fatalf("write formula: %v", err)
	}
	fmt.Println(*output)
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
