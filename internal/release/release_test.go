package release

import (
	"archive/tar"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildArchivePackagesWrapperBinaryAndGhosttyLibrary(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	binaryPath := filepath.Join(tempDir, "codelima")
	libraryPath := filepath.Join(tempDir, "libghostty-vt.dylib")
	outputPath := filepath.Join(tempDir, "dist", "artifact.tar.gz")

	if err := os.WriteFile(binaryPath, []byte("binary-data"), 0o755); err != nil {
		t.Fatalf("write binary: %v", err)
	}
	if err := os.WriteFile(libraryPath, []byte("ghostty-data"), 0o644); err != nil {
		t.Fatalf("write ghostty library: %v", err)
	}

	manifest, err := BuildArchive("1.2.3", "darwin", "arm64", binaryPath, libraryPath, outputPath)
	if err != nil {
		t.Fatalf("BuildArchive() error = %v", err)
	}

	if manifest.AssetName != "codelima_1.2.3_darwin_arm64.tar.gz" {
		t.Fatalf("unexpected asset name %q", manifest.AssetName)
	}
	if manifest.SHA256 == "" {
		t.Fatalf("expected sha256")
	}

	files := readArchiveFiles(t, outputPath)
	wrapperPath := "codelima_1.2.3_darwin_arm64/bin/codelima"
	binaryArchivePath := "codelima_1.2.3_darwin_arm64/bin/codelima-real"
	libArchivePath := "codelima_1.2.3_darwin_arm64/lib/libghostty-vt.dylib"

	wrapper, ok := files[wrapperPath]
	if !ok {
		t.Fatalf("missing wrapper %q", wrapperPath)
	}
	if !strings.Contains(string(wrapper), `CODELIMA_GHOSTTY_VT_LIB="${SCRIPT_DIR}/../lib/libghostty-vt.dylib"`) {
		t.Fatalf("wrapper did not reference darwin ghostty library: %s", wrapper)
	}
	if string(files[binaryArchivePath]) != "binary-data" {
		t.Fatalf("unexpected binary archive content %q", files[binaryArchivePath])
	}
	if string(files[libArchivePath]) != "ghostty-data" {
		t.Fatalf("unexpected library archive content %q", files[libArchivePath])
	}
}

func TestRenderHomebrewFormulaIncludesAvailableTargets(t *testing.T) {
	t.Parallel()

	formula, err := RenderHomebrewFormula(FormulaSpec{
		Repo: "brianrackle/codelima",
		Tag:  "v1.2.3",
		Manifests: []Manifest{
			{
				Version:   "1.2.3",
				GOOS:      "darwin",
				GOARCH:    "arm64",
				AssetName: "codelima_1.2.3_darwin_arm64.tar.gz",
				SHA256:    strings.Repeat("a", 64),
			},
			{
				Version:   "1.2.3",
				GOOS:      "linux",
				GOARCH:    "amd64",
				AssetName: "codelima_1.2.3_linux_amd64.tar.gz",
				SHA256:    strings.Repeat("b", 64),
			},
		},
	})
	if err != nil {
		t.Fatalf("RenderHomebrewFormula() error = %v", err)
	}

	for _, snippet := range []string{
		`require "zlib"`,
		`class Codelima < Formula`,
		`depends_on "git"`,
		`depends_on "lima"`,
		`version "1.2.3"`,
		`on_macos do`,
		`on_arm do`,
		`https://github.com/brianrackle/codelima/releases/download/v1.2.3/codelima_1.2.3_darwin_arm64.tar.gz`,
		`on_linux do`,
		`on_intel do`,
		`https://github.com/brianrackle/codelima/releases/download/v1.2.3/codelima_1.2.3_linux_amd64.tar.gz`,
		`ghostty_lib = OS.mac? ? "libghostty-vt.dylib" : "libghostty-vt.so"`,
		`root = Dir["codelima_*/bin/codelima-real"].empty? ? "." : Dir["codelima_*"].fetch(0)`,
		`odie "missing packaged release root" unless File.exist?(File.join(root, "bin", "codelima-real"))`,
		`Zlib::GzipWriter.open(pkgshare/"#{ghostty_lib}.gz") do |gz|`,
		`pkgshare.mkpath`,
		`CACHE_ROOT="${XDG_CACHE_HOME:-$HOME/.cache}/codelima/#{version}"`,
		`gzip -dc "#{pkgshare}/#{ghostty_lib}.gz" > "$RUNTIME_LIB.tmp"`,
		`export CODELIMA_GHOSTTY_VT_LIB="$RUNTIME_LIB"`,
	} {
		if !strings.Contains(formula, snippet) {
			t.Fatalf("formula missing %q:\n%s", snippet, formula)
		}
	}
}

func TestRenderHomebrewFormulaRejectsMixedVersions(t *testing.T) {
	t.Parallel()

	_, err := RenderHomebrewFormula(FormulaSpec{
		Repo: "brianrackle/codelima",
		Tag:  "v1.2.3",
		Manifests: []Manifest{
			{Version: "1.2.3", GOOS: "darwin", GOARCH: "arm64", AssetName: "one", SHA256: strings.Repeat("a", 64)},
			{Version: "1.2.4", GOOS: "linux", GOARCH: "amd64", AssetName: "two", SHA256: strings.Repeat("b", 64)},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "same version") {
		t.Fatalf("expected mixed version error, got %v", err)
	}
}

func readArchiveFiles(t *testing.T, archivePath string) map[string][]byte {
	t.Helper()

	file, err := os.Open(archivePath)
	if err != nil {
		t.Fatalf("open archive: %v", err)
	}
	defer func() {
		_ = file.Close()
	}()

	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		t.Fatalf("new gzip reader: %v", err)
	}
	defer func() {
		_ = gzipReader.Close()
	}()

	tarReader := tar.NewReader(gzipReader)
	files := make(map[string][]byte)
	for {
		header, err := tarReader.Next()
		if err != nil {
			if err == io.EOF {
				return files
			}
			t.Fatalf("read tar entry: %v", err)
		}
		data := make([]byte, header.Size)
		if _, err := io.ReadFull(tarReader, data); err != nil {
			t.Fatalf("read %s: %v", header.Name, err)
		}
		files[header.Name] = data
	}
}
