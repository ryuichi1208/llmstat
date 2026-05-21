package stats

import (
	"testing"
	"time"
)

func TestComputeITL_Empty(t *testing.T) {
	got := ComputeITL(nil)
	if got.Count != 0 {
		t.Fatalf("want zero ITLStats for nil, got %+v", got)
	}
}

func TestComputeITL_Single(t *testing.T) {
	got := ComputeITL([]time.Time{time.Now()})
	if got.Count != 0 {
		t.Fatalf("want zero ITLStats for single chunk, got %+v", got)
	}
}

func TestComputeITL_TwoChunks(t *testing.T) {
	t0 := time.Unix(0, 0)
	got := ComputeITL([]time.Time{t0, t0.Add(50 * time.Millisecond)})
	if got.Count != 1 {
		t.Fatalf("want Count=1, got %d", got.Count)
	}
	if got.Max != 50*time.Millisecond {
		t.Fatalf("want Max=50ms, got %v", got.Max)
	}
}

func TestComputeITL_MultipleChunks(t *testing.T) {
	t0 := time.Unix(0, 0)
	times := []time.Time{
		t0,
		t0.Add(10 * time.Millisecond),
		t0.Add(30 * time.Millisecond),
		t0.Add(60 * time.Millisecond),
		t0.Add(160 * time.Millisecond),
	}
	got := ComputeITL(times)
	if got.Count != 4 {
		t.Fatalf("want Count=4, got %d", got.Count)
	}
	if got.Max != 100*time.Millisecond {
		t.Fatalf("want Max=100ms, got %v", got.Max)
	}
}
