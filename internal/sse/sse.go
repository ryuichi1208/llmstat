// Package sse provides a minimal Server-Sent Events scanner.
//
// ScanSSE reads from r and emits one event per blank-line-terminated block.
// Only the "data" field is collected; comments, "event", "id", "retry" lines
// are skipped. Each emit callback receives the data bytes and the wall-clock
// time at which the blank line terminating the event was observed.
package sse

import (
	"bufio"
	"bytes"
	"io"
	"slices"
	"strings"
	"time"
)

type Event struct {
	Data []byte
}

func Scan(r io.Reader, emit func(Event, time.Time)) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)

	var data bytes.Buffer
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if data.Len() > 0 {
				emit(Event{Data: slices.Clone(data.Bytes())}, time.Now())
				data.Reset()
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		field, value, ok := splitField(line)
		if !ok {
			continue
		}
		if field != "data" {
			continue
		}
		if data.Len() > 0 {
			data.WriteByte('\n')
		}
		data.WriteString(value)
	}
	if data.Len() > 0 {
		emit(Event{Data: slices.Clone(data.Bytes())}, time.Now())
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return nil
}

func splitField(line string) (field, value string, ok bool) {
	idx := strings.IndexByte(line, ':')
	if idx < 0 {
		return line, "", true
	}
	field = line[:idx]
	value = line[idx+1:]
	value = strings.TrimPrefix(value, " ")
	return field, value, true
}
