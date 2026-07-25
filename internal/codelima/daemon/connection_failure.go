package daemon

import (
	"errors"
	"sync"
)

var errConnectionClosed = errors.New("daemon connection closed")

type connectionFailure struct {
	once sync.Once
	mu   sync.RWMutex

	cause  error
	record ConnectionCloseRecord
}

func (f *connectionFailure) Fail(record ConnectionCloseRecord, cause error) bool {
	accepted := false
	f.once.Do(func() {
		if cause == nil {
			cause = errConnectionClosed
		}
		f.mu.Lock()
		f.cause = cause
		f.record = record
		f.mu.Unlock()
		accepted = true
	})
	return accepted
}

func (f *connectionFailure) Cause() error {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.cause
}

func (f *connectionFailure) Record() ConnectionCloseRecord {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.record
}
