package codelima

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func captureSnapshot(project Project, snapshotID, kind, destinationTree string, excludes []string, createdAt time.Time) (SnapshotManifest, error) {
	if err := ensureDir(destinationTree); err != nil {
		return SnapshotManifest{}, err
	}

	entries := []SnapshotEntry{}
	var totalBytes int64

	walkErr := filepath.WalkDir(project.WorkspacePath, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		relativePath, err := filepath.Rel(project.WorkspacePath, path)
		if err != nil {
			return err
		}

		if relativePath == "." {
			return nil
		}

		normalized := normalizeWorkspaceRelative(relativePath)
		if shouldExcludePath(normalized, excludes) {
			if entry.IsDir() {
				return filepath.SkipDir
			}

			return nil
		}

		info, err := entry.Info()
		if err != nil {
			return err
		}

		targetPath := filepath.Join(destinationTree, relativePath)
		mode := uint32(info.Mode().Perm())

		switch {
		case entry.Type()&os.ModeSymlink != 0:
			linkTarget, err := os.Readlink(path)
			if err != nil {
				return err
			}

			if err := validateSymlinkTarget(project.WorkspacePath, path, linkTarget); err != nil {
				return err
			}

			if err := ensureDir(filepath.Dir(targetPath)); err != nil {
				return err
			}

			if err := os.Symlink(linkTarget, targetPath); err != nil {
				return err
			}

			entries = append(entries, SnapshotEntry{
				Path:       normalized,
				Type:       "symlink",
				Mode:       mode,
				LinkTarget: linkTarget,
			})
		case entry.IsDir():
			if err := ensureDir(targetPath); err != nil {
				return err
			}

			entries = append(entries, SnapshotEntry{
				Path: normalized,
				Type: "dir",
				Mode: mode,
			})
		case info.Mode().IsRegular():
			if err := copyFile(path, targetPath, info.Mode().Perm()); err != nil {
				return err
			}

			sum, err := fileSHA256(path)
			if err != nil {
				return err
			}

			totalBytes += info.Size()
			entries = append(entries, SnapshotEntry{
				Path:   normalized,
				Type:   "file",
				Mode:   mode,
				Size:   info.Size(),
				SHA256: sum,
			})
		default:
			return unsupportedFeature(
				"workspace contains unsupported filesystem object",
				map[string]any{"path": normalized},
			)
		}

		return nil
	})
	if walkErr != nil {
		return SnapshotManifest{}, walkErr
	}

	sortSnapshotEntries(entries)

	return SnapshotManifest{
		ID:            snapshotID,
		ProjectID:     project.ID,
		Kind:          kind,
		CreatedAt:     createdAt,
		WorkspacePath: project.WorkspacePath,
		EntryCount:    len(entries),
		TotalBytes:    totalBytes,
		Entries:       entries,
		TreeRoot:      destinationTree,
	}, nil
}

func materializeSnapshot(manifest SnapshotManifest, destinationPath string) error {
	if exists(destinationPath) {
		empty, err := directoryEmpty(destinationPath)
		if err != nil {
			return err
		}

		if !empty {
			return preconditionFailed("destination workspace must be empty", map[string]any{"path": destinationPath})
		}
	} else if err := ensureDir(destinationPath); err != nil {
		return err
	}

	for _, entry := range manifest.Entries {
		sourcePath := filepath.Join(manifest.TreeRoot, filepath.FromSlash(entry.Path))
		targetPath := filepath.Join(destinationPath, filepath.FromSlash(entry.Path))

		switch entry.Type {
		case "dir":
			if err := ensureDir(targetPath); err != nil {
				return err
			}
			if err := os.Chmod(targetPath, fs.FileMode(entry.Mode)); err != nil {
				return err
			}
		case "file":
			if err := copyFile(sourcePath, targetPath, fs.FileMode(entry.Mode)); err != nil {
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

func syncWorkspaceFromTree(existing SnapshotManifest, sourceTree string, workspacePath string) error {
	stageEntries, err := scanTreeEntries(sourceTree)
	if err != nil {
		return err
	}

	stageEntryMap := map[string]SnapshotEntry{}
	stagePaths := make([]string, 0, len(stageEntries))
	for _, entry := range stageEntries {
		stageEntryMap[entry.Path] = entry
		stagePaths = append(stagePaths, entry.Path)
	}

	existingPaths := make([]string, 0, len(existing.Entries))
	for _, entry := range existing.Entries {
		existingPaths = append(existingPaths, entry.Path)
	}

	sortPathsDesc(existingPaths)
	for _, path := range existingPaths {
		targetPath := filepath.Join(workspacePath, filepath.FromSlash(path))
		stageEntry, ok := stageEntryMap[path]
		currentEntry := findEntry(existing.Entries, path)
		if !ok || currentEntry.Type != stageEntry.Type {
			_ = os.RemoveAll(targetPath)
		}
	}

	sortPathsAsc(stagePaths)
	for _, path := range stagePaths {
		entry := stageEntryMap[path]
		sourcePath := filepath.Join(sourceTree, filepath.FromSlash(path))
		targetPath := filepath.Join(workspacePath, filepath.FromSlash(path))

		switch entry.Type {
		case "dir":
			if err := ensureDir(targetPath); err != nil {
				return err
			}
			if err := os.Chmod(targetPath, fs.FileMode(entry.Mode)); err != nil {
				return err
			}
		case "file":
			if err := copyFile(sourcePath, targetPath, fs.FileMode(entry.Mode)); err != nil {
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

func restoreWorkspace(manifest SnapshotManifest, workspacePath string) error {
	return syncWorkspaceFromTree(manifest, manifest.TreeRoot, workspacePath)
}

func scanTreeEntries(root string) ([]SnapshotEntry, error) {
	entries := []SnapshotEntry{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		relativePath, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}

		if relativePath == "." {
			return nil
		}

		info, err := entry.Info()
		if err != nil {
			return err
		}

		snapshotEntry := SnapshotEntry{
			Path: normalizeWorkspaceRelative(relativePath),
			Mode: uint32(info.Mode().Perm()),
		}

		switch {
		case entry.Type()&os.ModeSymlink != 0:
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}

			snapshotEntry.Type = "symlink"
			snapshotEntry.LinkTarget = target
		case entry.IsDir():
			snapshotEntry.Type = "dir"
		case info.Mode().IsRegular():
			sum, err := fileSHA256(path)
			if err != nil {
				return err
			}

			snapshotEntry.Type = "file"
			snapshotEntry.Size = info.Size()
			snapshotEntry.SHA256 = sum
		default:
			return unsupportedFeature("tree contains unsupported filesystem object", map[string]any{"path": relativePath})
		}

		entries = append(entries, snapshotEntry)
		return nil
	})
	if err != nil {
		return nil, err
	}

	sortSnapshotEntries(entries)
	return entries, nil
}

func shouldExcludePath(path string, excludes []string) bool {
	for _, exclude := range excludes {
		if path == exclude || strings.HasPrefix(path, exclude+"/") {
			return true
		}
	}

	return false
}

func validateSymlinkTarget(root, path, linkTarget string) error {
	resolved := linkTarget
	if !filepath.IsAbs(linkTarget) {
		resolved = filepath.Join(filepath.Dir(path), linkTarget)
	}

	resolved = filepath.Clean(resolved)
	root = filepath.Clean(root)

	if resolved != root && !strings.HasPrefix(resolved, root+string(os.PathSeparator)) {
		return unsupportedFeature(
			"symlink resolves outside workspace root",
			map[string]any{"path": path, "target": linkTarget},
		)
	}

	return nil
}

func sortSnapshotEntries(entries []SnapshotEntry) {
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Path < entries[j].Path
	})
}

func findEntry(entries []SnapshotEntry, path string) SnapshotEntry {
	for _, entry := range entries {
		if entry.Path == path {
			return entry
		}
	}

	return SnapshotEntry{}
}
