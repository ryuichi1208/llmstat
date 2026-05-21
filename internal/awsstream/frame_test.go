package awsstream

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

func TestEncodeDecode_RoundTrip(t *testing.T) {
	headers := []Header{
		{Name: ":event-type", Value: "chunk"},
		{Name: ":content-type", Value: "application/json"},
	}
	payload := []byte(`{"bytes":"ZGF0YQ=="}`)

	frame := EncodeFrame(headers, payload)

	dec := NewDecoder(bytes.NewReader(frame))
	msg, err := dec.Next()
	if err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	if got := msg.Header(":event-type"); got != "chunk" {
		t.Errorf("event-type want=chunk got=%q", got)
	}
	if got := msg.Header(":content-type"); got != "application/json" {
		t.Errorf("content-type want=application/json got=%q", got)
	}
	if !bytes.Equal(msg.Payload, payload) {
		t.Errorf("payload mismatch: got=%q want=%q", msg.Payload, payload)
	}
	if _, err := dec.Next(); !errors.Is(err, io.EOF) {
		t.Errorf("expected EOF after single frame, got %v", err)
	}
}

func TestDecode_MultipleFrames(t *testing.T) {
	var buf bytes.Buffer
	buf.Write(EncodeFrame([]Header{{Name: ":event-type", Value: "chunk"}}, []byte("one")))
	buf.Write(EncodeFrame([]Header{{Name: ":event-type", Value: "chunk"}}, []byte("two")))
	buf.Write(EncodeFrame([]Header{{Name: ":event-type", Value: "message_stop"}}, []byte("end")))

	dec := NewDecoder(&buf)
	var payloads []string
	for {
		m, err := dec.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		payloads = append(payloads, string(m.Payload))
	}
	want := []string{"one", "two", "end"}
	if len(payloads) != 3 {
		t.Fatalf("want 3 frames, got %d (%v)", len(payloads), payloads)
	}
	for i, w := range want {
		if payloads[i] != w {
			t.Errorf("frame[%d] want=%q got=%q", i, w, payloads[i])
		}
	}
}

func TestDecode_CorruptCRC(t *testing.T) {
	frame := EncodeFrame([]Header{{Name: ":event-type", Value: "chunk"}}, []byte("x"))
	// flip last byte to break message CRC
	frame[len(frame)-1] ^= 0xff
	dec := NewDecoder(bytes.NewReader(frame))
	if _, err := dec.Next(); err == nil {
		t.Errorf("expected CRC error")
	}
}
