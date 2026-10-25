package codelima

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"
	"time"
)

// Only these content-free facts cross into the selected-node preview. Paths,
// skipped-item reasons and credential contents stay out of the projection.
type fleetAuthImportEvidence struct {
	ImportedAt time.Time `json:"imported_at"`
	ItemCount  int       `json:"item_count"`
	Categories []string  `json:"categories,omitempty"`
}

func (s *Store) fleetAuthImport(node Node) *fleetAuthImportEvidence {
	if !node.ImportHostAuth || !node.AuthImportCompleted {
		return nil
	}
	data, _, _, err := readNodeLogTail(s.nodeEventsPath(node.ID), nodeLogByteLimit)
	if err != nil {
		return nil
	}
	var receipt *fleetAuthImportEvidence
	for line := range bytes.SplitSeq(data, []byte{'\n'}) {
		var event struct {
			Timestamp json.RawMessage `json:"timestamp"`
			Type      string          `json:"type"`
			Fields    json.RawMessage `json:"fields"`
		}
		if json.Unmarshal(line, &event) != nil || event.Type != "node.auth.imported" {
			continue
		}
		// The last complete import event wins, including invalid/unknown data.
		// Never show an older success as the result of a newer malformed event.
		receipt = nil
		var fields struct {
			Imported *[]string `json:"imported"`
		}
		var importedAt time.Time
		if json.Unmarshal(event.Timestamp, &importedAt) != nil || importedAt.IsZero() || json.Unmarshal(event.Fields, &fields) != nil || fields.Imported == nil {
			continue
		}
		seen, categories := map[string]bool{}, map[string]bool{}
		valid := true
		for _, name := range *fields.Imported {
			category := authArtifactCategory(name)
			if category == "" || seen[name] {
				valid = false
				break
			}
			seen[name], categories[category] = true, true
		}
		if !valid {
			continue
		}
		receipt = &fleetAuthImportEvidence{ImportedAt: importedAt, ItemCount: len(seen)}
		for _, category := range []string{"ssh", "git", "codex", "claude"} {
			if categories[category] {
				receipt.Categories = append(receipt.Categories, category)
			}
		}
	}
	return receipt
}

func authArtifactCategory(name string) string {
	switch name {
	case "known_hosts", "ssh_config":
		return "ssh"
	case "gitconfig":
		return "git"
	case "codex":
		return "codex"
	case "claude", "claude_state":
		return "claude"
	default:
		if slices.Contains(standardSSHIdentities, strings.TrimSuffix(name, ".pub")) {
			return "ssh"
		}
		return ""
	}
}
