package codelima

import "testing"

func TestRendererCheckpointQuotaBoundsReplacementAndRelease(t *testing.T) {
	pool := newRendererCheckpointQuota(100)
	if !pool.reserve("a", 70) || pool.reserve("b", 31) {
		t.Fatal("aggregate quota admission is incorrect")
	}
	if !pool.reserve("a", 40) || !pool.reserve("b", 60) {
		t.Fatal("replacement did not reclaim old retained bytes")
	}
	if pool.reserve("a", 41) {
		t.Fatal("replacement exceeded aggregate quota")
	}
	pool.release("b")
	if !pool.reserve("a", 100) {
		t.Fatal("release did not return retained budget")
	}
	pool.release("a")
	if pool.used != 0 || len(pool.owners) != 0 {
		t.Fatalf("quota leaked: %+v", pool)
	}
}
