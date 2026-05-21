// Package awsstream implements a minimal decoder for the AWS event-stream
// binary format used by services like Bedrock Runtime's
// invoke-with-response-stream endpoint.
//
// Wire format per frame:
//
//	prelude (12 bytes):
//	   total_length     uint32 BE
//	   headers_length   uint32 BE
//	   prelude_crc      uint32 BE  (CRC32 over the first 8 bytes of the prelude)
//	headers (variable, length = headers_length)
//	payload (variable, length = total_length - headers_length - 16)
//	message_crc          uint32 BE  (CRC32 over everything except itself)
//
// Each header in the headers block is encoded as:
//
//	name_len  uint8
//	name      [name_len]byte (UTF-8)
//	type      uint8
//	value     (variable, depends on type)
//
// This implementation handles the header types Bedrock uses in practice
// (string=7) and skips other types as best-effort.
package awsstream

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
)

type Header struct {
	Name  string
	Value string
}

type Message struct {
	Headers []Header
	Payload []byte
}

// Header returns the value of the first header matching name, or "".
func (m *Message) Header(name string) string {
	for _, h := range m.Headers {
		if h.Name == name {
			return h.Value
		}
	}
	return ""
}

// Decoder reads framed event-stream messages from an io.Reader.
type Decoder struct {
	r io.Reader
}

func NewDecoder(r io.Reader) *Decoder {
	return &Decoder{r: r}
}

// Next reads and returns the next message. Returns io.EOF when the stream
// ends cleanly between messages.
func (d *Decoder) Next() (*Message, error) {
	var prelude [12]byte
	if _, err := io.ReadFull(d.r, prelude[:]); err != nil {
		return nil, err
	}
	totalLen := binary.BigEndian.Uint32(prelude[0:4])
	headersLen := binary.BigEndian.Uint32(prelude[4:8])
	preludeCRC := binary.BigEndian.Uint32(prelude[8:12])

	if crc32.ChecksumIEEE(prelude[0:8]) != preludeCRC {
		return nil, errors.New("awsstream: prelude CRC mismatch")
	}
	if totalLen < 16 {
		return nil, fmt.Errorf("awsstream: total_length too small: %d", totalLen)
	}
	if headersLen > totalLen-16 {
		return nil, fmt.Errorf("awsstream: headers_length %d larger than total %d-16", headersLen, totalLen)
	}
	payloadLen := totalLen - headersLen - 16

	rest := make([]byte, totalLen-12)
	if _, err := io.ReadFull(d.r, rest); err != nil {
		return nil, err
	}

	headersBytes := rest[:headersLen]
	payload := rest[headersLen : headersLen+payloadLen]
	gotCRC := binary.BigEndian.Uint32(rest[len(rest)-4:])

	// message CRC covers prelude (12) + headers + payload
	crc := crc32.NewIEEE()
	crc.Write(prelude[:])
	crc.Write(rest[:len(rest)-4])
	if crc.Sum32() != gotCRC {
		return nil, errors.New("awsstream: message CRC mismatch")
	}

	headers, err := parseHeaders(headersBytes)
	if err != nil {
		return nil, err
	}
	return &Message{Headers: headers, Payload: payload}, nil
}

func parseHeaders(b []byte) ([]Header, error) {
	var out []Header
	for len(b) > 0 {
		if len(b) < 1 {
			return nil, errors.New("awsstream: truncated header name length")
		}
		nameLen := int(b[0])
		b = b[1:]
		if len(b) < nameLen+1 {
			return nil, errors.New("awsstream: truncated header name/type")
		}
		name := string(b[:nameLen])
		b = b[nameLen:]
		hType := b[0]
		b = b[1:]
		switch hType {
		case 7: // string
			if len(b) < 2 {
				return nil, errors.New("awsstream: truncated string header length")
			}
			vLen := int(binary.BigEndian.Uint16(b[:2]))
			b = b[2:]
			if len(b) < vLen {
				return nil, errors.New("awsstream: truncated string header value")
			}
			out = append(out, Header{Name: name, Value: string(b[:vLen])})
			b = b[vLen:]
		case 6: // byte array
			if len(b) < 2 {
				return nil, errors.New("awsstream: truncated bytes header length")
			}
			vLen := int(binary.BigEndian.Uint16(b[:2]))
			b = b[2+vLen:]
			// silently dropped — not interesting for our use case
		case 0, 1, 2: // bool true, bool false, byte
			if hType == 2 {
				if len(b) < 1 {
					return nil, errors.New("awsstream: truncated byte header")
				}
				b = b[1:]
			}
			out = append(out, Header{Name: name, Value: ""})
		case 3: // int16
			b = b[2:]
		case 4: // int32
			b = b[4:]
		case 5: // int64
			b = b[8:]
		case 8: // timestamp
			b = b[8:]
		case 9: // uuid
			b = b[16:]
		default:
			return nil, fmt.Errorf("awsstream: unsupported header type %d", hType)
		}
	}
	return out, nil
}

// EncodeFrame builds a single event-stream frame from headers and payload.
// Used in tests to build fixture streams.
func EncodeFrame(headers []Header, payload []byte) []byte {
	var hb []byte
	for _, h := range headers {
		hb = append(hb, byte(len(h.Name)))
		hb = append(hb, []byte(h.Name)...)
		hb = append(hb, 7) // string type
		hb = binary.BigEndian.AppendUint16(hb, uint16(len(h.Value)))
		hb = append(hb, []byte(h.Value)...)
	}
	headersLen := uint32(len(hb))
	totalLen := 16 + headersLen + uint32(len(payload))

	var prelude [12]byte
	binary.BigEndian.PutUint32(prelude[0:4], totalLen)
	binary.BigEndian.PutUint32(prelude[4:8], headersLen)
	binary.BigEndian.PutUint32(prelude[8:12], crc32.ChecksumIEEE(prelude[0:8]))

	out := make([]byte, 0, totalLen)
	out = append(out, prelude[:]...)
	out = append(out, hb...)
	out = append(out, payload...)

	crc := crc32.NewIEEE()
	crc.Write(out)
	out = binary.BigEndian.AppendUint32(out, crc.Sum32())
	return out
}
