package daemon

import (
	"context"
	"errors"
	"testing"
)

func TestOutboundQueuePrioritizesControlFrames(t *testing.T) {
	t.Parallel()

	queue := newOutboundQueue(4, 64)
	if !queue.TryPush([]byte("event"), outboundLow) {
		t.Fatal("enqueue low-priority frame")
	}
	if !queue.TryPush([]byte("response"), outboundHigh) {
		t.Fatal("enqueue high-priority frame")
	}

	first, err := queue.Pop(context.Background())
	if err != nil {
		t.Fatalf("Pop() error = %v", err)
	}
	if got := string(first); got != "response" {
		t.Fatalf("first frame = %q, want response", got)
	}
	second, err := queue.Pop(context.Background())
	if err != nil {
		t.Fatalf("second Pop() error = %v", err)
	}
	if got := string(second); got != "event" {
		t.Fatalf("second frame = %q, want event", got)
	}
}

func TestOutboundQueueBoundsFrameCountAndBytes(t *testing.T) {
	t.Parallel()

	queue := newOutboundQueue(2, 5)
	if !queue.TryPush([]byte("12"), outboundLow) {
		t.Fatal("enqueue first frame")
	}
	if !queue.TryPush([]byte("345"), outboundLow) {
		t.Fatal("enqueue frame at exact byte bound")
	}
	if queue.TryPush([]byte("x"), outboundHigh) {
		t.Fatal("queue accepted a frame beyond its count and byte bounds")
	}

	depth, bytes, _ := queue.Stats()
	if depth != 2 || bytes != 5 {
		t.Fatalf("queue stats = (%d frames, %d bytes), want (2, 5)", depth, bytes)
	}
}

func TestOutboundQueueCloseUnblocksPop(t *testing.T) {
	t.Parallel()

	queue := newOutboundQueue(1, 1)
	queue.Close()
	_, err := queue.Pop(context.Background())
	if !errors.Is(err, errOutboundQueueClosed) {
		t.Fatalf("Pop() error = %v, want errOutboundQueueClosed", err)
	}
}
