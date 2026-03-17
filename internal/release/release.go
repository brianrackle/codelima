package release

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	FormulaClassName = "Codelima"
	FormulaDesc      = "Shell-first TUI and CLI for Lima-backed project nodes"
	FormulaHomepage  = "https://github.com/brianrackle/codelima"
	FormulaLicense   = "GPL-3.0-only"
)

type Manifest struct {
	Version   string `json:"version"`
	GOOS      string `json:"goos"`
	GOARCH    string `json:"goarch"`
	AssetName string `json:"asset_name"`
	SHA256    string `json:"sha256"`
}

type FormulaSpec struct {
	Repo      string
	Tag       string
	Manifests []Manifest
}

func ArchiveBaseName(version, goos, goarch string) (string, error) {
	version = strings.TrimSpace(version)
	goos = strings.TrimSpace(goos)
	goarch = strings.TrimSpace(goarch)
	if version == "" {
		return "", fmt.Errorf("version is required")
	}
	if goos == "" {
		return "", fmt.Errorf("goos is required")
	}
	if goarch == "" {
		return "", fmt.Errorf("goarch is required")
	}
	return fmt.Sprintf("codelima_%s_%s_%s", version, goos, goarch), nil
}

func ArchiveName(version, goos, goarch string) (string, error) {
	base, err := ArchiveBaseName(version, goos, goarch)
	if err != nil {
		return "", err
	}
	return base + ".tar.gz", nil
}

func LibraryFilename(goos string) (string, error) {
	switch strings.TrimSpace(goos) {
	case "darwin":
		return "libghostty-vt.dylib", nil
	case "linux":
		return "libghostty-vt.so", nil
	default:
		return "", fmt.Errorf("unsupported goos %q", goos)
	}
}

func BuildArchive(version, goos, goarch, binaryPath, libraryPath, outputPath string) (Manifest, error) {
	assetName, err := ArchiveName(version, goos, goarch)
	if err != nil {
		return Manifest{}, err
	}
	rootName, err := ArchiveBaseName(version, goos, goarch)
	if err != nil {
		return Manifest{}, err
	}
	libFilename, err := LibraryFilename(goos)
	if err != nil {
		return Manifest{}, err
	}
	if strings.TrimSpace(binaryPath) == "" {
		return Manifest{}, fmt.Errorf("binary path is required")
	}
	if strings.TrimSpace(libraryPath) == "" {
		return Manifest{}, fmt.Errorf("ghostty library path is required")
	}
	if strings.TrimSpace(outputPath) == "" {
		return Manifest{}, fmt.Errorf("output path is required")
	}

	binaryData, binaryMode, err := readArchiveFile(binaryPath)
	if err != nil {
		return Manifest{}, fmt.Errorf("read binary: %w", err)
	}
	libraryData, libraryMode, err := readArchiveFile(libraryPath)
	if err != nil {
		return Manifest{}, fmt.Errorf("read ghostty library: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return Manifest{}, err
	}

	outFile, err := os.Create(outputPath)
	if err != nil {
		return Manifest{}, err
	}
	defer func() {
		_ = outFile.Close()
	}()

	hash := sha256.New()
	multiWriter := io.MultiWriter(outFile, hash)
	gzipWriter := gzip.NewWriter(multiWriter)
	tarWriter := tar.NewWriter(gzipWriter)

	if err := writeArchiveEntry(tarWriter, rootName+"/bin/codelima", []byte(wrapperScript(goos)), 0o755); err != nil {
		return Manifest{}, err
	}
	if err := writeArchiveEntry(tarWriter, rootName+"/bin/codelima-real", binaryData, binaryMode); err != nil {
		return Manifest{}, err
	}
	if err := writeArchiveEntry(tarWriter, rootName+"/lib/"+libFilename, libraryData, libraryMode); err != nil {
		return Manifest{}, err
	}
	if err := tarWriter.Close(); err != nil {
		return Manifest{}, err
	}
	if err := gzipWriter.Close(); err != nil {
		return Manifest{}, err
	}

	return Manifest{
		Version:   version,
		GOOS:      goos,
		GOARCH:    goarch,
		AssetName: assetName,
		SHA256:    hex.EncodeToString(hash.Sum(nil)),
	}, nil
}

func ReadManifest(path string) (Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, err
	}
	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return Manifest{}, err
	}
	if strings.TrimSpace(manifest.Version) == "" || strings.TrimSpace(manifest.GOOS) == "" || strings.TrimSpace(manifest.GOARCH) == "" || strings.TrimSpace(manifest.AssetName) == "" || strings.TrimSpace(manifest.SHA256) == "" {
		return Manifest{}, fmt.Errorf("manifest %s is incomplete", path)
	}
	return manifest, nil
}

func WriteManifest(path string, manifest Manifest) error {
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o644)
}

func RenderHomebrewFormula(spec FormulaSpec) (string, error) {
	repo := strings.TrimSpace(spec.Repo)
	tag := strings.TrimSpace(spec.Tag)
	if repo == "" {
		return "", fmt.Errorf("repo is required")
	}
	if tag == "" {
		return "", fmt.Errorf("tag is required")
	}
	if len(spec.Manifests) == 0 {
		return "", fmt.Errorf("at least one manifest is required")
	}

	assetsByTarget := map[string]map[string]Manifest{}
	versions := map[string]struct{}{}
	for _, manifest := range spec.Manifests {
		goos := strings.TrimSpace(manifest.GOOS)
		goarch := strings.TrimSpace(manifest.GOARCH)
		if _, err := LibraryFilename(goos); err != nil {
			return "", err
		}
		if goarch != "amd64" && goarch != "arm64" {
			return "", fmt.Errorf("unsupported goarch %q", goarch)
		}
		if _, ok := assetsByTarget[goos]; !ok {
			assetsByTarget[goos] = map[string]Manifest{}
		}
		if _, exists := assetsByTarget[goos][goarch]; exists {
			return "", fmt.Errorf("duplicate manifest for %s/%s", goos, goarch)
		}
		assetsByTarget[goos][goarch] = manifest
		versions[manifest.Version] = struct{}{}
	}
	if len(versions) != 1 {
		return "", fmt.Errorf("all manifests must use the same version")
	}

	var builder strings.Builder
	builder.WriteString("class ")
	builder.WriteString(FormulaClassName)
	builder.WriteString(" < Formula\n")
	builder.WriteString("  desc ")
	builder.WriteString(rubyString(FormulaDesc))
	builder.WriteString("\n")
	builder.WriteString("  homepage ")
	builder.WriteString(rubyString(FormulaHomepage))
	builder.WriteString("\n")
	builder.WriteString("  license ")
	builder.WriteString(rubyString(FormulaLicense))
	builder.WriteString("\n\n")

	for _, goos := range []string{"darwin", "linux"} {
		arches := assetsByTarget[goos]
		if len(arches) == 0 {
			continue
		}
		builder.WriteString("  on_")
		builder.WriteString(formulaOSBlock(goos))
		builder.WriteString(" do\n")
		for _, goarch := range []string{"arm64", "amd64"} {
			manifest, ok := arches[goarch]
			if !ok {
				continue
			}
			builder.WriteString("    ")
			builder.WriteString(formulaArchBlock(goarch))
			builder.WriteString(" do\n")
			builder.WriteString("      url ")
			builder.WriteString(rubyString(assetURL(repo, tag, manifest.AssetName)))
			builder.WriteString("\n")
			builder.WriteString("      sha256 ")
			builder.WriteString(rubyString(manifest.SHA256))
			builder.WriteString("\n")
			builder.WriteString("    end\n")
		}
		builder.WriteString("  end\n\n")
	}

	builder.WriteString("  depends_on ")
	builder.WriteString(rubyString("git"))
	builder.WriteString("\n")
	builder.WriteString("  depends_on ")
	builder.WriteString(rubyString("lima"))
	builder.WriteString("\n\n")
	builder.WriteString("  def install\n")
	builder.WriteString("    root = Dir[\"codelima_*\"]\n")
	builder.WriteString("    odie \"missing packaged release root\" if root.empty?\n")
	builder.WriteString("    root = root.fetch(0)\n")
	builder.WriteString("    ghostty_lib = OS.mac? ? \"libghostty-vt.dylib\" : \"libghostty-vt.so\"\n")
	builder.WriteString("    (libexec/\"bin\").install \"#{root}/bin/codelima-real\"\n")
	builder.WriteString("    (libexec/\"lib\").install \"#{root}/lib/#{ghostty_lib}\"\n")
	builder.WriteString("    (bin/\"codelima\").write <<~SH\n")
	builder.WriteString("#!/bin/bash\n")
	builder.WriteString("export CODELIMA_GHOSTTY_VT_LIB=\"#{libexec}/lib/#{ghostty_lib}\"\n")
	builder.WriteString("exec \"#{libexec}/bin/codelima-real\" \"$@\"\n")
	builder.WriteString("SH\n")
	builder.WriteString("    chmod 0755, bin/\"codelima\"\n")
	builder.WriteString("  end\n\n")
	builder.WriteString("  test do\n")
	builder.WriteString("    assert_match \"Usage:\", shell_output(\"#{bin}/codelima --help\")\n")
	builder.WriteString("  end\n")
	builder.WriteString("end\n")

	return builder.String(), nil
}

func readArchiveFile(path string) ([]byte, int64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, 0, err
	}
	mode := int64(info.Mode().Perm())
	if mode == 0 {
		mode = 0o644
	}
	return data, mode, nil
}

func writeArchiveEntry(tw *tar.Writer, name string, data []byte, mode int64) error {
	header := &tar.Header{
		Name: name,
		Mode: mode,
		Size: int64(len(data)),
	}
	if err := tw.WriteHeader(header); err != nil {
		return err
	}
	_, err := tw.Write(data)
	return err
}

func wrapperScript(goos string) string {
	libName, _ := LibraryFilename(goos)
	return strings.Join([]string{
		"#!/usr/bin/env sh",
		"set -eu",
		`SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)`,
		fmt.Sprintf(`export CODELIMA_GHOSTTY_VT_LIB="${SCRIPT_DIR}/../lib/%s"`, libName),
		`exec "${SCRIPT_DIR}/codelima-real" "$@"`,
		"",
	}, "\n")
}

func formulaArchBlock(goarch string) string {
	if goarch == "arm64" {
		return "on_arm"
	}
	return "on_intel"
}

func formulaOSBlock(goos string) string {
	if goos == "darwin" {
		return "macos"
	}
	return goos
}

func assetURL(repo, tag, assetName string) string {
	return fmt.Sprintf("https://github.com/%s/releases/download/%s/%s", repo, tag, assetName)
}

func rubyString(value string) string {
	return fmt.Sprintf("%q", value)
}

func SortedManifestPaths(paths []string) []string {
	sorted := append([]string(nil), paths...)
	sort.Strings(sorted)
	return sorted
}
