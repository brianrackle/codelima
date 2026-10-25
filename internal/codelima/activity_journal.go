package codelima

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

const (
	activityMessageRetention = 7 * 24 * time.Hour
	activityMessageLimit     = 10_000
	activityMessageFileLimit = 32 << 10
)

type activityMessageRequest struct {
	Action     activityAction `json:"action,omitempty"`
	TerminalID string         `json:"terminal_id,omitempty"`
	Success    bool           `json:"success,omitempty"`
	ID         string         `json:"id"`
	Source     string         `json:"source"`
	Level      slog.Level     `json:"level"`
	Text       string         `json:"text"`
	NodeID     string         `json:"node_id,omitempty"`
	NodeSlug   string         `json:"node_slug,omitempty"`
}

// ActivityMessage separates a producer's stable idempotency key and content
// from the time and sequence observed by the daemon that committed it.
type ActivityMessage struct {
	activityMessageRequest
	Sequence uint64    `json:"sequence"`
	Time     time.Time `json:"time"`
}

// The daemon's process lock supplies the single writer. Individual private
// records use the same atomic publication boundary as operation receipts.
// Epoch plus sequence lets a frontend overlay acknowledged messages until its
// next history snapshot includes them, without duplicating older pages.
type activityJournal struct {
	mu      sync.Mutex
	service *Service
	dir     string
	epoch   string
	through uint64
	limit   int
	records map[string]ActivityMessage
}

func newActivityJournal(service *Service) (*activityJournal, error) {
	j := &activityJournal{service: service, dir: filepath.Join(service.cfg.MetadataRoot, "_activity"), epoch: newID(), limit: activityMessageLimit, records: make(map[string]ActivityMessage)}
	if err := os.MkdirAll(j.dir, 0o700); err != nil {
		return nil, err
	}
	info, err := os.Lstat(j.dir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("activity journal must be a private directory: %w", errors.Join(err, errors.New("invalid activity directory")))
	}
	if err := os.Chmod(j.dir, 0o700); err != nil {
		return nil, err
	}
	files, err := os.ReadDir(j.dir)
	if err != nil {
		return nil, err
	}
	sequences := make(map[uint64]bool)
	for _, file := range files {
		id, ok := strings.CutSuffix(file.Name(), ".json")
		if !ok {
			continue
		}
		if _, err := uuid.Parse(id); err != nil {
			continue
		}
		data, offset, missing, err := readNodeLogTail(filepath.Join(j.dir, file.Name()), activityMessageFileLimit)
		if err != nil || missing || offset != 0 {
			return nil, fmt.Errorf("read activity message %s: %w", id, errors.Join(err, errors.New("message unavailable or oversized")))
		}
		var record ActivityMessage
		if err := json.Unmarshal(data, &record); err != nil {
			return nil, fmt.Errorf("invalid activity message %s: %w", id, err)
		}
		request, err := normalizeActivityMessage(record.activityMessageRequest)
		if err != nil || request != record.activityMessageRequest || record.ID != id || record.Sequence == 0 || record.Time.IsZero() || sequences[record.Sequence] {
			return nil, fmt.Errorf("invalid activity message %s", id)
		}
		sequences[record.Sequence] = true
		j.through = max(j.through, record.Sequence)
		j.records[id] = record
	}
	if err := j.pruneLocked(); err != nil {
		return nil, err
	}
	return j, nil
}

func normalizeActivityMessage(request activityMessageRequest) (activityMessageRequest, error) {
	if _, err := uuid.Parse(request.ID); err != nil {
		return request, invalidArgument("activity message ID must be a UUID", nil)
	}
	if request.NodeID != "" {
		if _, err := uuid.Parse(request.NodeID); err != nil {
			return request, invalidArgument("activity node ID must be a UUID", nil)
		}
	}
	if request.Level != slog.LevelInfo && request.Level != slog.LevelWarn && request.Level != slog.LevelError {
		return request, invalidArgument("activity level must be info, warn or error", nil)
	}
	if err := validateActivityAction(request); err != nil {
		return request, err
	}
	for _, field := range []struct {
		value string
		limit int
	}{{request.Text, 4096}, {request.Source, 64}, {request.NodeSlug, 128}} {
		if len(field.value) > field.limit || !utf8.ValidString(field.value) {
			return request, invalidArgument("activity message field is oversized or invalid UTF-8", nil)
		}
	}
	request.Text = strings.TrimSpace(plainLogText(request.Text))
	request.Source = strings.TrimSpace(plainLogText(request.Source))
	request.NodeSlug = strings.TrimSpace(plainLogText(request.NodeSlug))
	if request.Text == "" || request.Source == "" {
		return request, invalidArgument("activity source and text are required", nil)
	}
	if len(request.Text) > 4096 || len(request.Source) > 64 || len(request.NodeSlug) > 128 {
		return request, invalidArgument("normalized activity message exceeds its field limit", nil)
	}
	return request, nil
}

func (j *activityJournal) Append(request activityMessageRequest) (ActivityMessage, error) {
	request, err := normalizeActivityMessage(request)
	if err != nil {
		return ActivityMessage{}, err
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	// A prior eviction failure permits at most one excess record. Retry that
	// cleanup before admitting more, so a broken filesystem cannot grow it.
	if err := j.pruneLocked(); err != nil {
		return ActivityMessage{}, err
	}
	if existing, ok := j.records[request.ID]; ok {
		if existing.activityMessageRequest != request {
			return ActivityMessage{}, preconditionFailed("activity message ID already belongs to different content", nil)
		}
		return existing, nil
	}
	record := ActivityMessage{activityMessageRequest: request, Sequence: j.through + 1, Time: j.service.now().UTC()}
	if record.Sequence == 0 {
		return ActivityMessage{}, metadataCorruption("activity sequence exhausted", nil, nil)
	}
	data, err := json.Marshal(record)
	if err != nil {
		return ActivityMessage{}, err
	}
	if len(data) > activityMessageFileLimit {
		return ActivityMessage{}, invalidArgument("activity message exceeds its storage limit", nil)
	}
	if err := atomicWriteFile(filepath.Join(j.dir, record.ID+".json"), data, 0o600); err != nil {
		return ActivityMessage{}, err
	}
	j.records[record.ID], j.through = record, record.Sequence
	return record, j.pruneLocked()
}

func (j *activityJournal) Snapshot() ([]ActivityMessage, uint64) {
	j.mu.Lock()
	defer j.mu.Unlock()
	now := j.service.now()
	records := make([]ActivityMessage, 0, len(j.records))
	for _, record := range j.records {
		if now.Sub(record.Time) <= activityMessageRetention {
			records = append(records, record)
		}
	}
	slices.SortFunc(records, func(a, b ActivityMessage) int {
		if a.Sequence > b.Sequence {
			return -1
		}
		if a.Sequence < b.Sequence {
			return 1
		}
		return 0
	})
	return records, j.through
}

func (j *activityJournal) Prune() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.pruneLocked()
}

func (j *activityJournal) pruneLocked() error {
	now := j.service.now()
	remove := func(record ActivityMessage) error {
		if err := os.Remove(filepath.Join(j.dir, record.ID+".json")); err != nil && !os.IsNotExist(err) {
			return err
		}
		delete(j.records, record.ID)
		return nil
	}
	for _, record := range j.records {
		if now.Sub(record.Time) > activityMessageRetention {
			if err := remove(record); err != nil {
				return err
			}
		}
	}
	limit := max(1, j.limit)
	if len(j.records) <= limit {
		return nil
	}
	records := make([]ActivityMessage, 0, len(j.records))
	for _, record := range j.records {
		records = append(records, record)
	}
	slices.SortFunc(records, func(a, b ActivityMessage) int {
		if a.Sequence < b.Sequence {
			return -1
		}
		if a.Sequence > b.Sequence {
			return 1
		}
		return 0
	})
	for _, record := range records[:len(records)-limit] {
		if err := remove(record); err != nil {
			return err
		}
	}
	return nil
}
