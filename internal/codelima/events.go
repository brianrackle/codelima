package codelima

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

type Event struct {
	Timestamp time.Time      `json:"timestamp"`
	Type      string         `json:"type"`
	Message   string         `json:"message,omitempty"`
	Fields    map[string]any `json:"fields,omitempty"`
}

const (
	// eventsReadBufferSize is the starting per-record read budget for an events
	// log, matching the Lima output scanner's initial buffer.
	eventsReadBufferSize = 64 * 1024

	// eventsRecordLimit caps a single retained record. It mirrors the Lima
	// parser's enlarged ceiling so a long but valid record still decodes,
	// instead of the default 64KiB bufio.Scanner limit failing the whole log
	// forever the moment one record grew past it.
	eventsRecordLimit = limaOutputLimit
)

func appendEvent(path string, event Event) error {
	if err := ensureDir(filepathDir(path)); err != nil {
		return err
	}

	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer func() {
		_ = file.Close()
	}()

	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}

	if _, err := file.Write(append(payload, '\n')); err != nil {
		return err
	}

	return nil
}

// readEvents parses an append-only events log defensively. The log is as often
// crash debris as it is clean state, so a single damaged record must never make
// the rest unreadable: undecodable records are skipped, an oversized record is
// dropped without stopping the scan, and a read fault mid-file still yields the
// records already parsed. Only failing to open the file is an error.
func readEvents(path string, logger *slog.Logger) ([]Event, error) {
	if !exists(path) {
		return []Event{}, nil
	}
	if logger == nil {
		logger = packageLog()
	}

	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = file.Close()
	}()

	reader := bufio.NewReaderSize(file, eventsReadBufferSize)
	events := []Event{}
	line := 0
	for {
		record, terminated, oversized, readErr := readEventRecord(reader)
		if oversized || terminated || len(record) > 0 {
			line++
			trimmed := bytes.TrimSpace(record)
			switch {
			case oversized:
				logSkippedEventRecord(logger, path, line, terminated,
					errors.New("record exceeds the maximum event record size"))
			case len(trimmed) == 0:
				// A blank separator line carries nothing to skip or report.
			default:
				var event Event
				if decodeErr := json.Unmarshal(trimmed, &event); decodeErr != nil {
					logSkippedEventRecord(logger, path, line, terminated, decodeErr)
					break
				}
				events = append(events, event)
			}
		}

		if readErr != nil {
			if !errors.Is(readErr, io.EOF) {
				logger.Warn("stopping event log read after a read error",
					"path", path, "line", line, "error", readErr)
			}

			return events, nil
		}
	}
}

// readEventRecord reads one newline-delimited record. terminated reports whether
// the record ended with a newline; only the final record can be unterminated,
// and that means the append was torn. oversized reports that the record ran past
// eventsRecordLimit and was discarded — the reader still consumes it to the next
// newline so the records after it stay readable, which a bufio.Scanner hitting
// its token limit cannot do.
func readEventRecord(reader *bufio.Reader) (record []byte, terminated bool, oversized bool, err error) {
	for {
		chunk, readErr := reader.ReadSlice('\n')
		if !oversized {
			if len(record)+len(chunk) > eventsRecordLimit {
				oversized = true
				record = nil
			} else {
				record = append(record, chunk...)
			}
		}

		switch {
		case errors.Is(readErr, bufio.ErrBufferFull):
			continue
		case readErr != nil:
			return record, false, oversized, readErr
		default:
			return record, true, oversized, nil
		}
	}
}

// logSkippedEventRecord separates the two shapes of damage. An unterminated
// final record is the expected debris of a process killed mid-append and is not
// worth alarming about; a fully written record that will not decode means the
// file was damaged after the fact, which is worth a warning.
func logSkippedEventRecord(logger *slog.Logger, path string, line int, terminated bool, err error) {
	if !terminated {
		logger.Debug("discarding torn trailing event record", "path", path, "line", line, "error", err)
		return
	}

	logger.Warn("skipping corrupt event record", "path", path, "line", line, "error", err)
}

func filepathDir(path string) string {
	return filepath.Dir(path)
}
