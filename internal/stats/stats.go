// Package stats collects per-request latency stats and renders them in a
// httpstat-style timeline plus stream stats (TTFT, inter-chunk latency,
// throughput).
package stats

import (
	"crypto/tls"
	"net/http/httptrace"
	"slices"
	"time"
)

type Stats struct {
	ReqStart time.Time

	AuthDuration time.Duration // pre-request auth (e.g. Vertex token exchange). May be 0.

	DNSStart, DNSDone         time.Time
	ConnectStart, ConnectDone time.Time
	TLSStart, TLSDone         time.Time
	GotFirstByte              time.Time
	FirstChunk                time.Time
	LastChunk                 time.Time
	EOF                       time.Time

	ChunkTimes  []time.Time
	Tokens      int // output tokens
	InputTokens int
	ThinkTokens int
	TotalTokens int
	Status      int
	Model       string
	Provider    string
	URL         string
	ErrorMsg    string
}

func NewClientTrace(s *Stats) *httptrace.ClientTrace {
	return &httptrace.ClientTrace{
		DNSStart: func(_ httptrace.DNSStartInfo) {
			s.DNSStart = time.Now()
		},
		DNSDone: func(_ httptrace.DNSDoneInfo) {
			s.DNSDone = time.Now()
		},
		ConnectStart: func(_, _ string) {
			s.ConnectStart = time.Now()
		},
		ConnectDone: func(_, _ string, _ error) {
			s.ConnectDone = time.Now()
		},
		TLSHandshakeStart: func() {
			s.TLSStart = time.Now()
		},
		TLSHandshakeDone: func(_ tls.ConnectionState, _ error) {
			s.TLSDone = time.Now()
		},
		GotFirstResponseByte: func() {
			s.GotFirstByte = time.Now()
		},
	}
}

type ITLStats struct {
	P50, P95, Max time.Duration
	Count         int
}

// ComputeITL returns inter-chunk latency percentiles for the given chunk
// arrival timestamps. Returns a zero ITLStats when fewer than 2 chunks were
// observed (no gaps to measure).
func ComputeITL(times []time.Time) ITLStats {
	if len(times) < 2 {
		return ITLStats{}
	}
	gaps := make([]time.Duration, 0, len(times)-1)
	for i := 1; i < len(times); i++ {
		gaps = append(gaps, times[i].Sub(times[i-1]))
	}
	slices.Sort(gaps)
	pick := func(p float64) time.Duration {
		idx := int(float64(len(gaps)-1) * p)
		return gaps[idx]
	}
	return ITLStats{
		P50:   pick(0.50),
		P95:   pick(0.95),
		Max:   gaps[len(gaps)-1],
		Count: len(gaps),
	}
}

func dur(a, b time.Time) time.Duration {
	if a.IsZero() || b.IsZero() {
		return 0
	}
	return b.Sub(a)
}
