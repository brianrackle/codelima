package codelima

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	operationOutputLines     = 200
	operationOutputBytes     = 32 << 10
	operationOutputLineBytes = 4096
)

// A command's stdout/stderr share one bounded tail. Disk writes are coalesced
// independently of input chunk frequency, and completion flushes the last tail.
type nodeOperationOutput struct {
	manager        *nodeOperations
	operation      *runningNodeOperation
	mu             sync.Mutex
	lines          []string
	partial        []byte
	partialClipped bool
	bytes          int
	next           uint64
	version        uint64
	saved          uint64
	err            error
	stop           chan struct{}
	done           chan struct{}
	closeOnce      sync.Once
}

func newNodeOperationOutput(manager *nodeOperations, operation *runningNodeOperation) *nodeOperationOutput {
	w := &nodeOperationOutput{manager: manager, operation: operation, stop: make(chan struct{}), done: make(chan struct{})}
	go func() {
		defer close(w.done)
		tick := time.NewTicker(time.Second)
		defer tick.Stop()
		for {
			select {
			case <-w.stop:
				return
			case <-tick.C:
				if err := w.flush(); err != nil {
					w.mu.Lock()
					w.err = err
					w.mu.Unlock()
					manager.mu.Lock()
					if operation.cancel != nil {
						operation.cancel(err)
					}
					manager.mu.Unlock()
					return
				}
			}
		}
	}()
	return w
}

func (w *nodeOperationOutput) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n := len(data)
	if n == 0 {
		return 0, nil
	}
	w.version++
	for len(data) > 0 {
		end := bytes.IndexAny(data, "\r\n")
		if end < 0 {
			w.appendPartial(data)
			break
		}
		w.appendPartial(data[:end])
		line := w.partialText()
		w.lines = append(w.lines, line)
		w.bytes += len(line)
		w.next++
		w.partial = w.partial[:0]
		w.partialClipped = false
		data = data[end+1:]
		w.trim()
	}
	w.trim()
	return n, nil
}

func (w *nodeOperationOutput) appendPartial(data []byte) {
	const limit = operationOutputLineBytes - len("…")
	if len(data) >= limit {
		w.partial = append(w.partial[:0], data[len(data)-limit:]...)
		w.partialClipped = true
		return
	}
	if excess := len(w.partial) + len(data) - limit; excess > 0 {
		copy(w.partial, w.partial[excess:])
		w.partial = w.partial[:len(w.partial)-excess]
		w.partialClipped = true
	}
	w.partial = append(w.partial, data...)
}

func (w *nodeOperationOutput) partialText() string {
	text := strings.ToValidUTF8(string(w.partial), "�")
	if w.partialClipped {
		text = "…" + text
	}
	if len(text) > operationOutputLineBytes {
		start := len(text) - operationOutputLineBytes + len("…")
		for start < len(text) && !utf8.RuneStart(text[start]) {
			start++
		}
		text = "…" + text[start:]
	}
	return text
}

func (w *nodeOperationOutput) trim() {
	lines := operationOutputLines
	if len(w.partial) > 0 {
		lines--
	}
	for len(w.lines) > 0 && (len(w.lines) > lines || w.bytes+len(w.partialText()) > operationOutputBytes) {
		w.bytes -= len(w.lines[0])
		w.lines[0] = ""
		w.lines = w.lines[1:]
	}
}

func (w *nodeOperationOutput) flush() error {
	w.mu.Lock()
	if w.saved == w.version {
		w.mu.Unlock()
		return nil
	}
	version := w.version
	lines := append([]string(nil), w.lines...)
	partial := w.partialText()
	start := w.next - uint64(len(lines))
	w.mu.Unlock()
	w.manager.mu.Lock()
	w.operation.record.Output = lines
	w.operation.record.OutputStart = start
	w.operation.record.OutputPartial = partial
	err := w.manager.persist(w.operation.record)
	w.manager.mu.Unlock()
	if err == nil {
		w.mu.Lock()
		w.saved = version
		w.mu.Unlock()
	}
	return err
}

func (w *nodeOperationOutput) Close() error {
	w.closeOnce.Do(func() { close(w.stop) })
	<-w.done
	err := w.flush()
	w.mu.Lock()
	defer w.mu.Unlock()
	return errors.Join(w.err, err)
}

func operationOutputTee(original io.Writer, output io.Writer) io.Writer {
	if original == nil {
		return output
	}
	return io.MultiWriter(original, output)
}
