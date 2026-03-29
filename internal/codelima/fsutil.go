package codelima

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/google/uuid"
	"gopkg.in/yaml.v3"
)

func newID() string {
	id, err := uuid.NewV7()
	if err != nil {
		return uuid.NewString()
	}

	return id.String()
}

func shortID(id string) string {
	if len(id) < 8 {
		return id
	}

	return strings.ReplaceAll(id[:8], "-", "")
}

func ensureDir(path string) error {
	return os.MkdirAll(path, 0o755)
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func canonicalPath(path string) (string, error) {
	return filepath.Abs(filepath.Clean(expandHome(path)))
}

func atomicWriteFile(path string, data []byte, mode fs.FileMode) error {
	if err := ensureDir(filepath.Dir(path)); err != nil {
		return err
	}

	tempFile, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}

	tempPath := tempFile.Name()
	success := false
	defer func() {
		if success {
			return
		}

		_ = os.Remove(tempPath)
	}()

	if _, err := tempFile.Write(data); err != nil {
		_ = tempFile.Close()
		return err
	}

	if err := tempFile.Chmod(mode); err != nil {
		_ = tempFile.Close()
		return err
	}

	if err := tempFile.Close(); err != nil {
		return err
	}

	if err := os.Rename(tempPath, path); err != nil {
		return err
	}

	success = true
	return nil
}

func yamlBytes(value any) ([]byte, error) {
	return yaml.Marshal(value)
}

func writeYAMLFile(path string, value any) error {
	data, err := yamlBytes(value)
	if err != nil {
		return err
	}

	return atomicWriteFile(path, data, 0o644)
}

func readYAMLFile(path string, value any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	return yaml.Unmarshal(data, value)
}

func writeJSONFile(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}

	data = append(data, '\n')
	return atomicWriteFile(path, data, 0o644)
}

func readJSONFile(path string, value any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	return json.Unmarshal(data, value)
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() {
		_ = file.Close()
	}()

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}

	return hex.EncodeToString(hash.Sum(nil)), nil
}

func copyFile(src, dst string, mode fs.FileMode) error {
	if err := ensureDir(filepath.Dir(dst)); err != nil {
		return err
	}

	source, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() {
		_ = source.Close()
	}()

	target, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}

	if _, err := io.Copy(target, source); err != nil {
		_ = target.Close()
		return err
	}

	return target.Close()
}

func copySymlink(src, dst string) error {
	target, err := os.Readlink(src)
	if err != nil {
		return err
	}

	if err := ensureDir(filepath.Dir(dst)); err != nil {
		return err
	}

	_ = os.RemoveAll(dst)
	return os.Symlink(target, dst)
}

func directoryEmpty(path string) (bool, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return false, err
	}

	return len(entries) == 0, nil
}

func createTempDir(root, pattern string) (string, error) {
	if strings.TrimSpace(root) == "" {
		return os.MkdirTemp("", pattern)
	}

	if err := ensureDir(root); err != nil {
		return "", err
	}

	return os.MkdirTemp(root, pattern)
}

func slugify(input string) string {
	input = strings.ToLower(strings.TrimSpace(input))
	if input == "" {
		return "project"
	}

	var builder strings.Builder
	lastDash := false
	for _, character := range input {
		switch {
		case character >= 'a' && character <= 'z', character >= '0' && character <= '9':
			builder.WriteRune(character)
			lastDash = false
		case character == '-' || character == '_' || character == ' ' || character == '/':
			if builder.Len() > 0 && !lastDash {
				builder.WriteByte('-')
				lastDash = true
			}
		}
	}

	slug := strings.Trim(builder.String(), "-")
	if slug == "" {
		return "project"
	}

	return slug
}

func normalizeWorkspaceRelative(path string) string {
	return strings.TrimPrefix(filepath.ToSlash(path), "./")
}

func sortPathsDesc(paths []string) {
	sort.Slice(paths, func(i, j int) bool {
		if strings.Count(paths[i], "/") == strings.Count(paths[j], "/") {
			return paths[i] > paths[j]
		}

		return strings.Count(paths[i], "/") > strings.Count(paths[j], "/")
	})
}

func sortPathsAsc(paths []string) {
	sort.Slice(paths, func(i, j int) bool {
		if strings.Count(paths[i], "/") == strings.Count(paths[j], "/") {
			return paths[i] < paths[j]
		}

		return strings.Count(paths[i], "/") < strings.Count(paths[j], "/")
	})
}
