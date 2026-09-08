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

func TestBuildArchivePackagesOnlyCLIAndStaticRenderer(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	binaryPath := filepath.Join(tempDir, "codelima")
	rendererPath := filepath.Join(tempDir, "codelima-renderer-worker")
	buildID := strings.Repeat("a", 64)
	outputPath := filepath.Join(tempDir, "dist", "artifact.tar.gz")

	if err := os.WriteFile(binaryPath, []byte("binary-data"), 0o755); err != nil {
		t.Fatalf("write binary: %v", err)
	}
	if err := os.WriteFile(rendererPath, []byte("renderer-data"), 0o755); err != nil {
		t.Fatalf("write renderer: %v", err)
	}
	manifest, err := BuildArchive("1.2.3", "darwin", "arm64", binaryPath, rendererPath, buildID, outputPath)
	if err != nil {
		t.Fatalf("BuildArchive() error = %v", err)
	}

	if manifest.AssetName != "codelima_1.2.3_darwin_arm64.tar.gz" {
		t.Fatalf("unexpected asset name %q", manifest.AssetName)
	}
	if manifest.SHA256 == "" {
		t.Fatalf("expected sha256")
	}
	if manifest.RendererBuildID != buildID {
		t.Fatalf("native build identity = %q, want %q", manifest.RendererBuildID, buildID)
	}

	files := readArchiveFiles(t, outputPath)
	binaryArchivePath := "codelima_1.2.3_darwin_arm64/bin/codelima"
	rendererArchivePath := "codelima_1.2.3_darwin_arm64/bin/codelima-renderer-worker"
	if len(files) != 2 {
		t.Fatalf("static archive must contain exactly two executables, got %v", files)
	}
	if string(files[binaryArchivePath].data) != "binary-data" {
		t.Fatalf("unexpected binary archive content %q", files[binaryArchivePath].data)
	}
	if string(files[rendererArchivePath].data) != "renderer-data" {
		t.Fatalf("unexpected renderer archive content %q", files[rendererArchivePath].data)
	}
}

func TestBuildArchiveForcesPackagedExecutablesExecutable(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	binaryPath := filepath.Join(tempDir, "codelima")
	rendererPath := filepath.Join(tempDir, "codelima-renderer-worker")
	outputPath := filepath.Join(tempDir, "dist", "artifact.tar.gz")

	if err := os.WriteFile(binaryPath, []byte("binary-data"), 0o644); err != nil {
		t.Fatalf("write binary: %v", err)
	}
	if err := os.WriteFile(rendererPath, []byte("renderer-data"), 0o644); err != nil {
		t.Fatalf("write renderer: %v", err)
	}
	if _, err := BuildArchive("1.2.3", "darwin", "arm64", binaryPath, rendererPath, strings.Repeat("a", 64), outputPath); err != nil {
		t.Fatalf("BuildArchive() error = %v", err)
	}

	files := readArchiveFiles(t, outputPath)
	for _, archivePath := range []string{
		"codelima_1.2.3_darwin_arm64/bin/codelima",
		"codelima_1.2.3_darwin_arm64/bin/codelima-renderer-worker",
	} {
		if got := files[archivePath].mode; got != 0o755 {
			t.Fatalf("expected %s to be mode 0755, got %#o", archivePath, got)
		}
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
		`root = Dir["codelima_*/bin/codelima"].empty? ? "." : Dir["codelima_*"].fetch(0)`,
		`odie "missing packaged release root" unless File.exist?(File.join(root, "bin", "codelima"))`,
		`odie "missing packaged renderer worker" unless File.exist?(File.join(root, "bin", "codelima-renderer-worker"))`,
		`chmod 0755, libexec/"bin/codelima"`,
		`(libexec/"bin").install "#{root}/bin/codelima-renderer-worker"`,
		`chmod 0755, libexec/"bin/codelima-renderer-worker"`,
		`assert_predicate libexec/"bin/codelima-renderer-worker", :executable?`,
		`bin.install_symlink libexec/"bin/codelima"`,
	} {
		if !strings.Contains(formula, snippet) {
			t.Fatalf("formula missing %q:\n%s", snippet, formula)
		}
	}
	for _, obsolete := range []string{"zlib", "CODELIMA_GHOSTTY_VT_LIB", "CACHE_ROOT", "codelima-real", "libghostty-vt.so", "libghostty-vt.dylib"} {
		if strings.Contains(formula, obsolete) {
			t.Fatalf("formula retained obsolete dynamic dependency %q", obsolete)
		}
	}
}

func TestBuildArchiveRejectsInvalidRendererIdentity(t *testing.T) {
	for _, id := range []string{"", "abc", strings.Repeat("z", 64), strings.Repeat("A", 64)} {
		if _, err := BuildArchive("1.2.3", "linux", "arm64", "unused-cli", "unused-worker", id, "unused-output"); err == nil || !strings.Contains(err.Error(), "renderer build identity") {
			t.Fatalf("invalid build identity %q accepted or wrong error: %v", id, err)
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

func TestReleaseRejectsUnqualifiedDarwinAMD64(t *testing.T) {
	t.Parallel()
	if err := ValidateTarget("darwin", "amd64"); err == nil || !strings.Contains(err.Error(), "unsupported Lima runtime target") {
		t.Fatalf("ValidateTarget(darwin/amd64) error = %v", err)
	}
	if err := ValidateTarget("darwin", "arm64"); err != nil {
		t.Fatalf("ValidateTarget(darwin/arm64) error = %v", err)
	}
	if err := ValidateTarget("linux", "amd64"); err != nil {
		t.Fatalf("ValidateTarget(linux/amd64) error = %v", err)
	}
	if err := ValidateTarget("linux", "arm64"); err != nil {
		t.Fatalf("ValidateTarget(linux/arm64) error = %v", err)
	}
}

type archiveFile struct {
	data []byte
	mode int64
}

func readArchiveFiles(t *testing.T, archivePath string) map[string]archiveFile {
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
	files := make(map[string]archiveFile)
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
		files[header.Name] = archiveFile{
			data: data,
			mode: header.Mode,
		}
	}
}
