package kiro

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
)

// AWS event-stream framing:
// [totalLen u32][headersLen u32][preludeCRC u32][headers][payload][messageCRC u32]
const (
	preludeLen  = 12
	minFrameLen = 16
	maxFrameLen = 16 << 20
)

type Frame struct {
	Headers map[string]any
	Payload []byte
}

func (f Frame) header(name string) string {
	s, _ := f.Headers[name].(string)
	return s
}

func (f Frame) MessageType() string { return f.header(":message-type") }
func (f Frame) EventType() string   { return f.header(":event-type") }

// Decoder accepts the response body in arbitrary chunks. A bad prelude drops one
// byte and resynchronizes; a bad message CRC drops the whole frame (kiro.rs decoder.rs).
type Decoder struct {
	buf     []byte
	Skipped int
}

func (d *Decoder) Feed(chunk []byte) []Frame {
	d.buf = append(d.buf, chunk...)
	var frames []Frame
	for len(d.buf) >= preludeLen {
		total := int(binary.BigEndian.Uint32(d.buf[0:4]))
		headersLen := int(binary.BigEndian.Uint32(d.buf[4:8]))
		validPrelude := crc32.ChecksumIEEE(d.buf[0:8]) == binary.BigEndian.Uint32(d.buf[8:12])
		if !validPrelude || total < minFrameLen || total > maxFrameLen || headersLen > total-minFrameLen {
			d.buf = d.buf[1:]
			d.Skipped++
			continue
		}
		if len(d.buf) < total {
			break
		}
		frame := d.buf[:total]
		d.buf = d.buf[total:]
		if crc32.ChecksumIEEE(frame[:total-4]) != binary.BigEndian.Uint32(frame[total-4:]) {
			d.Skipped++
			continue
		}
		headers, err := decodeHeaders(frame[preludeLen : preludeLen+headersLen])
		if err != nil {
			d.Skipped++
			continue
		}
		frames = append(frames, Frame{Headers: headers, Payload: frame[preludeLen+headersLen : total-4]})
	}
	if len(d.buf) == 0 {
		d.buf = nil
	}
	return frames
}

var errShortHeader = errors.New("truncated event-stream header")

func decodeHeaders(b []byte) (map[string]any, error) {
	out := map[string]any{}
	for i := 0; i < len(b); {
		nameLen := int(b[i])
		i++
		if i+nameLen+1 > len(b) {
			return nil, errShortHeader
		}
		name := string(b[i : i+nameLen])
		i += nameLen
		typ := b[i]
		i++
		size, err := headerValueSize(typ, b[i:])
		if err != nil {
			return nil, err
		}
		if i+size > len(b) {
			return nil, errShortHeader
		}
		out[name] = headerValue(typ, b[i:i+size])
		i += size
	}
	return out, nil
}

func headerValueSize(typ byte, rest []byte) (int, error) {
	switch typ {
	case 0, 1:
		return 0, nil
	case 2:
		return 1, nil
	case 3:
		return 2, nil
	case 4:
		return 4, nil
	case 5, 8:
		return 8, nil
	case 9:
		return 16, nil
	case 6, 7:
		if len(rest) < 2 {
			return 0, errShortHeader
		}
		return 2 + int(binary.BigEndian.Uint16(rest)), nil
	default:
		return 0, fmt.Errorf("unknown event-stream header type %d", typ)
	}
}

func headerValue(typ byte, v []byte) any {
	switch typ {
	case 0:
		return true
	case 1:
		return false
	case 2:
		return int8(v[0])
	case 3:
		return int16(binary.BigEndian.Uint16(v))
	case 4:
		return int32(binary.BigEndian.Uint32(v))
	case 5, 8:
		return int64(binary.BigEndian.Uint64(v))
	case 6:
		return append([]byte(nil), v[2:]...)
	case 7:
		return string(v[2:])
	default:
		return append([]byte(nil), v...)
	}
}
