//go:build packageintegration && (darwin || linux)

package codelima

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/brianrackle/codelima/internal/release"
	"github.com/brianrackle/codelima/internal/testutil"
)

// This test deliberately uses the release executables, not a helper process
// compiled from the test package. make test-package supplies a fresh archive.
func TestPackagedStaticRenderer(t *testing.T) {
	dist := os.Getenv("CODELIMA_PACKAGE_TEST_DIST")
	if dist == "" {
		t.Fatal("run make test-package to build the native release pair")
	}
	manifests, err := filepath.Glob(filepath.Join(dist, "*.json"))
	if err != nil || len(manifests) != 1 {
		t.Fatalf("expected one release manifest: %v, %v", manifests, err)
	}
	manifest, err := release.ReadManifest(manifests[0])
	if err != nil {
		t.Fatal(err)
	}
	if manifest.RendererBuildID != rendererNativeBuildIdentity() {
		t.Fatalf("packaged identity %s differs from supervisor %s", manifest.RendererBuildID, rendererNativeBuildIdentity())
	}
	archive, err := os.Open(filepath.Join(dist, manifest.AssetName))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = archive.Close() }()
	digest := sha256.New()
	if _, err := io.Copy(digest, archive); err != nil {
		t.Fatal(err)
	}
	if hex.EncodeToString(digest.Sum(nil)) != manifest.SHA256 {
		t.Fatal("archive checksum differs from manifest")
	}
	if _, err := archive.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	compressed, err := gzip.NewReader(archive)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = compressed.Close() }()
	root := testutil.TempDir(t, "static-package-")
	reader := tar.NewReader(compressed)
	base := strings.TrimSuffix(manifest.AssetName, ".tar.gz")
	paths := make(map[string]string)
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		name := filepath.Base(header.Name)
		if header.Typeflag != tar.TypeReg || header.Name != base+"/bin/"+name || (name != "codelima" && name != rendererWorkerExecutableName) || paths[name] != "" || header.Size > 128<<20 {
			t.Fatalf("unexpected release archive entry: %+v", header)
		}
		data, err := io.ReadAll(reader)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(root, name)
		if header.Mode != 0o755 {
			t.Fatalf("%s has non-executable release mode %#o", name, header.Mode)
		}
		if err := os.WriteFile(path, data, 0o755); err != nil {
			t.Fatal(err)
		}
		paths[name] = path
		info, err := buildinfo.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s Go build provenance: %v", name, err)
		}
		foundID := false
		cgoEnabled := ""
		for _, setting := range info.Settings {
			if setting.Key == "CGO_ENABLED" {
				cgoEnabled = setting.Value
			}
			if setting.Key == "-ldflags" {
				foundID = strings.Contains(setting.Value, "rendererbuild.identityOverride="+manifest.RendererBuildID) && strings.Contains(setting.Value, "codelima.Version="+manifest.Version)
			}
		}
		if !foundID {
			t.Fatalf("%s did not embed the release version and native identity", name)
		}
		wantCGO := "1"
		if name == "codelima" && runtime.GOOS != "darwin" {
			wantCGO = "0"
		}
		if cgoEnabled != wantCGO {
			t.Fatalf("%s CGO_ENABLED = %q, want %q for the platform host capability/renderer boundary", name, cgoEnabled, wantCGO)
		}
	}
	if len(paths) != 2 {
		t.Fatalf("release must contain only the CLI and native worker: %v", paths)
	}
	emptyPath := filepath.Join(root, "empty-path")
	if err := os.Mkdir(emptyPath, 0o700); err != nil {
		t.Fatal(err)
	}
	environment := []string{
		"PATH=" + emptyPath,
		"CODELIMA_GHOSTTY_VT_LIB=" + filepath.Join(root, "does-not-exist.so"),
		"LD_LIBRARY_PATH=" + emptyPath,
		"DYLD_LIBRARY_PATH=" + emptyPath,
		"DYLD_FALLBACK_LIBRARY_PATH=" + emptyPath,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	for _, argument := range []string{"--help", "--version"} {
		command := exec.CommandContext(ctx, paths["codelima"], argument)
		command.Env = environment
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("packaged CLI %s without external tools: %v, %s", argument, err, output)
		}
		if argument == "--version" && strings.TrimSpace(string(output)) != manifest.Version {
			t.Fatalf("CLI version = %q, manifest = %s", output, manifest.Version)
		}
	}
	worker := paths[rendererWorkerExecutableName]
	link, err := startRendererLink(rendererProcessOptions{Executable: worker, Env: environment, CommandTimeout: 5 * time.Second, QueueFrames: 8}, 1, func(rendererWorkerFrame) {})
	if err != nil {
		t.Fatal(err)
	}
	defer link.Fail(errors.New("release smoke test complete"))
	result, err := link.CallResult(ctx, "init", rendererInitParams{TerminalID: "package-smoke", Cols: 40, Rows: 4})
	if err != nil {
		t.Fatalf("initialize packaged static renderer: %v", err)
	}
	if err := verifyRendererProtocol(worker, result); err != nil {
		t.Fatal(err)
	}
	var initialized rendererInitResult
	if err := json.Unmarshal(result, &initialized); err != nil || initialized.BuildID != manifest.RendererBuildID {
		t.Fatalf("native worker/manifest build mismatch: %s, %v", result, err)
	}
	const marker = "packaged-static-ghostty"
	if err := link.Call(ctx, "output", rendererOutputParams{EventID: 1, Data: []byte(marker)}); err != nil {
		t.Fatal(err)
	}
	result, err = link.CallResult(ctx, "read", rendererReadParams{})
	var rendered ReadResultDTO
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(result, &rendered); err != nil || rendered.Error != "" || !strings.Contains(rendered.Text, marker) {
		t.Fatalf("packaged native renderer did not preserve output: %s, %v", result, err)
	}
}
