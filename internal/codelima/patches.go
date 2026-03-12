package codelima

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func buildPatch(ctx context.Context, baseTree, sourceTree string) ([]byte, DiffSummary, error) {
	tempRoot, err := os.MkdirTemp("", "codelima-diff-*")
	if err != nil {
		return nil, DiffSummary{}, err
	}
	defer func() {
		_ = os.RemoveAll(tempRoot)
	}()

	baseDir := filepath.Join(tempRoot, "base")
	sourceDir := filepath.Join(tempRoot, "source")
	if err := copyTree(baseTree, baseDir); err != nil {
		return nil, DiffSummary{}, err
	}

	if err := copyTree(sourceTree, sourceDir); err != nil {
		return nil, DiffSummary{}, err
	}

	cmd := exec.CommandContext(ctx, "git", "diff", "--no-index", "--binary", "--src-prefix=a/", "--dst-prefix=b/", "base", "source")
	cmd.Dir = tempRoot
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()

	if err == nil {
		return nil, DiffSummary{}, preconditionFailed("patch proposal is a no-op", nil)
	}

	var exitErr *exec.ExitError
	if !As(err, &exitErr) || exitErr.ExitCode() != 1 {
		return nil, DiffSummary{}, externalCommandFailed(
			"git diff failed",
			fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String())),
			nil,
		)
	}

	patchBytes := rewritePatchPaths(stdout.Bytes())
	summary := summarizePatch(patchBytes)
	if summary.FilesChanged == 0 {
		return nil, DiffSummary{}, preconditionFailed("patch proposal is a no-op", nil)
	}

	return patchBytes, summary, nil
}

func rewritePatchPaths(patch []byte) []byte {
	text := string(patch)
	replacements := []struct {
		old string
		new string
	}{
		{"a/base/", "a/"},
		{"b/source/", "b/"},
		{"--- a/base/", "--- a/"},
		{"+++ b/source/", "+++ b/"},
	}

	for _, replacement := range replacements {
		text = strings.ReplaceAll(text, replacement.old, replacement.new)
	}

	return []byte(text)
}

func summarizePatch(patch []byte) DiffSummary {
	lines := strings.Split(string(patch), "\n")
	summary := DiffSummary{
		Paths: []string{},
	}
	pathSet := map[string]struct{}{}

	currentPath := ""
	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "diff --git a/"):
			parts := strings.Split(line, " ")
			if len(parts) >= 4 {
				currentPath = strings.TrimPrefix(parts[2], "a/")
				if _, ok := pathSet[currentPath]; !ok {
					pathSet[currentPath] = struct{}{}
					summary.Paths = append(summary.Paths, currentPath)
					summary.FilesChanged++
				}
			}
		case strings.HasPrefix(line, "new file mode "):
			summary.AddedFiles++
		case strings.HasPrefix(line, "deleted file mode "):
			summary.DeletedFiles++
		case strings.HasPrefix(line, "index ") && currentPath != "":
			// The diff header is the easiest stable signal for a regular modification.
		}
	}

	summary.ModifiedFiles = summary.FilesChanged - summary.AddedFiles - summary.DeletedFiles
	if summary.ModifiedFiles < 0 {
		summary.ModifiedFiles = 0
	}

	return summary
}

func applyPatchChecked(ctx context.Context, patchPath string, currentTarget SnapshotManifest) (string, error) {
	stageDir, err := os.MkdirTemp("", "codelima-apply-stage-*")
	if err != nil {
		return "", err
	}

	if err := copyTree(currentTarget.TreeRoot, stageDir); err != nil {
		_ = os.RemoveAll(stageDir)
		return "", err
	}

	checkCmd := exec.CommandContext(ctx, "git", "apply", "--check", patchPath)
	checkCmd.Dir = stageDir
	var checkStderr bytes.Buffer
	checkCmd.Stderr = &checkStderr
	if err := checkCmd.Run(); err != nil {
		_ = os.RemoveAll(stageDir)
		return "", patchConflict("patch apply preflight failed", map[string]any{"details": strings.TrimSpace(checkStderr.String())})
	}

	applyCmd := exec.CommandContext(ctx, "git", "apply", patchPath)
	applyCmd.Dir = stageDir
	var applyStderr bytes.Buffer
	applyCmd.Stderr = &applyStderr
	if err := applyCmd.Run(); err != nil {
		_ = os.RemoveAll(stageDir)
		return "", externalCommandFailed(
			"git apply failed after preflight",
			fmt.Errorf("%w: %s", err, strings.TrimSpace(applyStderr.String())),
			map[string]any{"patch_path": patchPath},
		)
	}

	return stageDir, nil
}

func copyTree(sourceRoot, targetRoot string) error {
	if err := ensureDir(targetRoot); err != nil {
		return err
	}

	entries, err := scanTreeEntries(sourceRoot)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		sourcePath := filepath.Join(sourceRoot, filepath.FromSlash(entry.Path))
		targetPath := filepath.Join(targetRoot, filepath.FromSlash(entry.Path))

		switch entry.Type {
		case "dir":
			if err := ensureDir(targetPath); err != nil {
				return err
			}
		case "file":
			if err := copyFile(sourcePath, targetPath, os.FileMode(entry.Mode)); err != nil {
				return err
			}
		case "symlink":
			if err := copySymlink(sourcePath, targetPath); err != nil {
				return err
			}
		default:
			return metadataCorruption("unknown snapshot entry type", nil, map[string]any{"type": entry.Type})
		}
	}

	return nil
}
