//go:build darwin || linux

package terminalio

import (
	"errors"
	"io"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/creack/pty"
	"golang.org/x/sys/unix"
)

func TestResizePreservesPTYFlagsAndReportsClosedDescriptor(t *testing.T) {
	master, slave, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = master.Close(); _ = slave.Close() })
	fd, err := Descriptor(master)
	if err != nil {
		t.Fatal(err)
	}
	if err := unix.SetNonblock(fd, true); err != nil {
		t.Fatal(err)
	}
	before, err := unix.FcntlInt(uintptr(fd), unix.F_GETFL, 0)
	if err != nil {
		t.Fatal(err)
	}
	size := unix.Winsize{Col: 110, Row: 24, Xpixel: 1100, Ypixel: 480}
	if err := Resize(master, &size); err != nil {
		t.Fatal(err)
	}
	after, err := unix.FcntlInt(uintptr(fd), unix.F_GETFL, 0)
	if err != nil || before != after {
		t.Fatalf("resize changed flags: before=%#x after=%#x err=%v", before, after, err)
	}
	got, err := unix.IoctlGetWinsize(fd, unix.TIOCGWINSZ)
	if err != nil || *got != size {
		t.Fatalf("resize did not apply geometry: got=%v err=%v", got, err)
	}
	if err := master.Close(); err != nil {
		t.Fatal(err)
	}
	if err := Resize(master, &size); err == nil {
		t.Fatalf("resize closed PTY: %v", err)
	}
}

type blockedWriteTarget struct {
	started   chan struct{}
	closed    chan struct{}
	startOnce sync.Once
	closeOnce sync.Once
}

func (w *blockedWriteTarget) Write([]byte) (int, error) {
	w.startOnce.Do(func() { close(w.started) })
	<-w.closed
	return 0, os.ErrClosed
}
func (w *blockedWriteTarget) Close() error { w.closeOnce.Do(func() { close(w.closed) }); return nil }
func (*blockedWriteTarget) Fd() uintptr    { return 0 }

func TestWriterBoundsQueuedInputAndCloseUnblocksWriter(t *testing.T) {
	target := &blockedWriteTarget{started: make(chan struct{}), closed: make(chan struct{})}
	writer := NewWriter(target, nil, nil)
	defer writer.Close()
	if !writer.Enqueue([]byte("active")) {
		t.Fatal("first input was not admitted")
	}
	select {
	case <-target.started:
	case <-time.After(time.Second):
		t.Fatal("writer did not start")
	}
	if !writer.Enqueue(make([]byte, QueueMaxBytes)) {
		t.Fatal("bounded queue capacity was not admitted")
	}
	if writer.Enqueue([]byte("overflow")) {
		t.Fatal("overflow was admitted")
	}
	if err := writer.Drain(time.Millisecond); err == nil {
		t.Fatal("blocked writer reported successful drain")
	}
	closed := make(chan struct{})
	go func() { writer.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("close did not unblock active writer")
	}
	if writer.Enqueue([]byte("after close")) {
		t.Fatal("closed queue admitted input")
	}
}

func TestReadNormalizesEndOfFileWithoutNativeDependency(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reader.Close() }()
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	fd, err := Descriptor(reader)
	if err != nil {
		t.Fatal(err)
	}
	n, err := Read(fd, make([]byte, 4))
	if n != 0 || !errors.Is(err, io.EOF) {
		t.Fatalf("closed peer read = %d, %v", n, err)
	}
}
