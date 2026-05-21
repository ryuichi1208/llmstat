package sse

import (
	"strings"
	"testing"
	"time"
)

func collect(t *testing.T, input string) [][]byte {
	t.Helper()
	var got [][]byte
	err := Scan(strings.NewReader(input), func(ev Event, _ time.Time) {
		got = append(got, ev.Data)
	})
	if err != nil {
		t.Fatalf("Scan returned error: %v", err)
	}
	return got
}

func TestSingleEvent(t *testing.T) {
	got := collect(t, "data: hello\n\n")
	if len(got) != 1 || string(got[0]) != "hello" {
		t.Fatalf("want [hello], got %q", got)
	}
}

func TestMultiLineData(t *testing.T) {
	got := collect(t, "data: line1\ndata: line2\n\n")
	if len(got) != 1 || string(got[0]) != "line1\nline2" {
		t.Fatalf("want [line1\\nline2], got %q", got)
	}
}

func TestMultipleEvents(t *testing.T) {
	got := collect(t, "data: a\n\ndata: b\n\ndata: c\n\n")
	if len(got) != 3 {
		t.Fatalf("want 3 events, got %d (%q)", len(got), got)
	}
	for i, want := range []string{"a", "b", "c"} {
		if string(got[i]) != want {
			t.Errorf("event[%d]: want %q, got %q", i, want, got[i])
		}
	}
}

func TestIgnoresNonDataFields(t *testing.T) {
	got := collect(t, "event: ping\nid: 1\nretry: 5000\ndata: payload\n\n")
	if len(got) != 1 || string(got[0]) != "payload" {
		t.Fatalf("want [payload], got %q", got)
	}
}

func TestIgnoresCommentLines(t *testing.T) {
	got := collect(t, ": comment\ndata: hello\n\n")
	if len(got) != 1 || string(got[0]) != "hello" {
		t.Fatalf("want [hello], got %q", got)
	}
}

func TestFlushOnEOFWithoutTrailingBlank(t *testing.T) {
	got := collect(t, "data: tail")
	if len(got) != 1 || string(got[0]) != "tail" {
		t.Fatalf("want [tail], got %q", got)
	}
}
