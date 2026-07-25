package codelima

import (
	"sync"
	"testing"
	"time"
)

func TestCoalescedPublisherBoundsBurstCadence(t *testing.T) {
	t.Parallel()

	const interval = 50 * time.Millisecond
	var (
		mu        sync.Mutex
		published []time.Time
	)
	publishedWake := make(chan struct{}, 8)
	publisher := newCoalescedPublisher(interval, func() {
		mu.Lock()
		published = append(published, time.Now())
		mu.Unlock()
		publishedWake <- struct{}{}
	})
	go publisher.Run(t.Context())

	for range 100 {
		publisher.Request()
	}
	waitForPublication(t, publishedWake)
	for range 100 {
		publisher.Request()
	}
	waitForPublication(t, publishedWake)

	select {
	case <-publishedWake:
		t.Fatal("one burst produced more than two publications")
	case <-time.After(2 * interval):
	}

	mu.Lock()
	defer mu.Unlock()
	if len(published) != 2 {
		t.Fatalf("publication count = %d, want 2", len(published))
	}
	if separation := published[1].Sub(published[0]); separation < interval {
		t.Fatalf("publication separation = %s, want at least %s", separation, interval)
	}
}

func waitForPublication(t *testing.T, wake <-chan struct{}) {
	t.Helper()
	select {
	case <-wake:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for publication")
	}
}
