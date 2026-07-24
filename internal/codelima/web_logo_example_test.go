package codelima

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCodeLimaLogoWebExampleContract(t *testing.T) {
	t.Parallel()

	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller() failed")
	}

	examplePath := filepath.Join(filepath.Dir(currentFile), "..", "..", "examples", "codelima-logo-animation.html")
	raw, err := os.ReadFile(examplePath)
	if err != nil {
		t.Fatalf("ReadFile(web logo example) error = %v", err)
	}

	content := string(raw)
	required := []string{
		`<!doctype html>`,
		`data-codelima-logo`,
		`aria-label="CodeLima"`,
		`width: 8ch`,
		`const TARGET = "CodeLima";`,
		`const GLYPHS = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789";`,
		`const SETTLE_INTERVAL_MS = 1000 / 3;`,
		`const SPIN_INTERVAL_MS = SETTLE_INTERVAL_MS / 6;`,
		`Math.floor(elapsedMs / SETTLE_INTERVAL_MS)`,
		`Math.floor(elapsedMs / SPIN_INTERVAL_MS)`,
		`const rate = (index % 3) + 1;`,
		`let glyphIndex = (frame * rate + index * 11) % GLYPHS.length;`,
		`if (GLYPHS[glyphIndex] === TARGET[index])`,
		`window.matchMedia("(prefers-reduced-motion: reduce)")`,
		`window.requestAnimationFrame`,
		`window.cancelAnimationFrame`,
		`window.animateCodeLimaLogo = animateCodeLimaLogo;`,
	}
	for _, fragment := range required {
		if !strings.Contains(content, fragment) {
			t.Errorf("web logo example does not contain required fragment %q", fragment)
		}
	}
}
