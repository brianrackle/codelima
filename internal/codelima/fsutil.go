package codelima

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/brianrackle/codelima/internal/atomicfile"
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

func ensureDir(path string) error {
	return os.MkdirAll(path, 0o755)
}

// exists reports whether path is statable. Errors other than "not exist"
// (permission, I/O) count as existing: callers branch on this to decide
// whether to seed or overwrite state, and inaccessible state must fail loudly
// at the operation that touches it rather than be silently treated as absent.
func exists(path string) bool {
	_, err := os.Stat(path)
	if err == nil {
		return true
	}
	return !os.IsNotExist(err)
}

func canonicalPath(path string) (string, error) {
	return filepath.Abs(filepath.Clean(expandHome(path)))
}

func atomicWriteFile(path string, data []byte, mode fs.FileMode) error {
	return atomicfile.WriteFile(path, data, mode)
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

func slugify(input string) string {
	input = strings.ToLower(strings.TrimSpace(input))
	if input == "" {
		return "node"
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
		return "node"
	}

	return slug
}
