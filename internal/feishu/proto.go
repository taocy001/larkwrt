package feishu

import "fmt"

// Minimal protobuf encode/decode for the Feishu WS Frame and FrameHeader
// messages. Implements only the field types actually used (varint, bytes/string,
// repeated message). Avoids adding a protobuf library dependency.

const (
	pbWireVarint   = 0
	pbWireLenDelim = 2
)

// ── Encode ────────────────────────────────────────────────────────────────────

func pbVarint(v uint64) []byte {
	var buf [10]byte
	n := 0
	for v >= 0x80 {
		buf[n] = byte(v) | 0x80
		v >>= 7
		n++
	}
	buf[n] = byte(v)
	return buf[:n+1]
}

func pbTag(fieldNum, wireType int) []byte {
	return pbVarint(uint64(fieldNum<<3 | wireType))
}

func pbUint64(buf []byte, fieldNum int, v uint64) []byte {
	buf = append(buf, pbTag(fieldNum, pbWireVarint)...)
	return append(buf, pbVarint(v)...)
}

func pbInt32(buf []byte, fieldNum int, v int32) []byte {
	return pbUint64(buf, fieldNum, uint64(v))
}

func pbBytes(buf []byte, fieldNum int, data []byte) []byte {
	buf = append(buf, pbTag(fieldNum, pbWireLenDelim)...)
	buf = append(buf, pbVarint(uint64(len(data)))...)
	return append(buf, data...)
}

func pbString(buf []byte, fieldNum int, s string) []byte {
	return pbBytes(buf, fieldNum, []byte(s))
}

func marshalHeader(h FrameHeader) []byte {
	var buf []byte
	buf = pbString(buf, 1, h.Key)
	buf = pbString(buf, 2, h.Value)
	return buf
}

func marshalFrame(f Frame) []byte {
	var buf []byte
	// Required fields always present (even if zero)
	buf = pbUint64(buf, 1, f.SeqID)
	buf = pbUint64(buf, 2, f.LogID)
	buf = pbInt32(buf, 3, f.Service)
	buf = pbInt32(buf, 4, f.Method)
	for _, h := range f.Headers {
		buf = pbBytes(buf, 5, marshalHeader(h))
	}
	if f.PayloadEncoding != "" {
		buf = pbString(buf, 6, f.PayloadEncoding)
	}
	if f.PayloadType != "" {
		buf = pbString(buf, 7, f.PayloadType)
	}
	if len(f.Payload) > 0 {
		buf = pbBytes(buf, 8, f.Payload)
	}
	if f.LogIDNew != "" {
		buf = pbString(buf, 9, f.LogIDNew)
	}
	return buf
}

// ── Decode ────────────────────────────────────────────────────────────────────

func pbDecodeVarint(buf []byte, pos int) (uint64, int) {
	var v uint64
	shift := uint(0)
	for pos < len(buf) {
		b := buf[pos]
		pos++
		v |= uint64(b&0x7f) << shift
		if b < 0x80 {
			return v, pos
		}
		shift += 7
		if shift > 63 {
			return 0, -1
		}
	}
	return 0, -1
}

func unmarshalHeader(buf []byte) (FrameHeader, error) {
	var h FrameHeader
	pos := 0
	for pos < len(buf) {
		tag, newPos := pbDecodeVarint(buf, pos)
		if newPos < 0 {
			return h, fmt.Errorf("bad varint in header")
		}
		pos = newPos
		fieldNum := int(tag >> 3)
		if int(tag&7) != pbWireLenDelim {
			return h, fmt.Errorf("unexpected wire type in header field %d", fieldNum)
		}
		length, newPos := pbDecodeVarint(buf, pos)
		if newPos < 0 {
			return h, fmt.Errorf("bad length in header")
		}
		pos = newPos
		end := pos + int(length)
		if end > len(buf) {
			return h, fmt.Errorf("header truncated")
		}
		val := string(buf[pos:end])
		pos = end
		switch fieldNum {
		case 1:
			h.Key = val
		case 2:
			h.Value = val
		}
	}
	return h, nil
}

func unmarshalFrame(buf []byte) (Frame, error) {
	var f Frame
	pos := 0
	for pos < len(buf) {
		tag, newPos := pbDecodeVarint(buf, pos)
		if newPos < 0 {
			return f, fmt.Errorf("bad varint in frame at pos %d", pos)
		}
		pos = newPos
		fieldNum := int(tag >> 3)
		wireType := int(tag & 7)

		switch wireType {
		case pbWireVarint:
			val, newPos := pbDecodeVarint(buf, pos)
			if newPos < 0 {
				return f, fmt.Errorf("bad varint value at field %d", fieldNum)
			}
			pos = newPos
			switch fieldNum {
			case 1:
				f.SeqID = val
			case 2:
				f.LogID = val
			case 3:
				f.Service = int32(val)
			case 4:
				f.Method = int32(val)
			}

		case pbWireLenDelim:
			length, newPos := pbDecodeVarint(buf, pos)
			if newPos < 0 {
				return f, fmt.Errorf("bad length at field %d", fieldNum)
			}
			pos = newPos
			end := pos + int(length)
			if end > len(buf) {
				return f, fmt.Errorf("frame truncated at field %d", fieldNum)
			}
			data := buf[pos:end]
			pos = end
			switch fieldNum {
			case 5:
				h, err := unmarshalHeader(data)
				if err != nil {
					return f, err
				}
				f.Headers = append(f.Headers, h)
			case 6:
				f.PayloadEncoding = string(data)
			case 7:
				f.PayloadType = string(data)
			case 8:
				f.Payload = append([]byte(nil), data...)
			case 9:
				f.LogIDNew = string(data)
			}

		default:
			return f, fmt.Errorf("unknown wire type %d at field %d", wireType, fieldNum)
		}
	}
	return f, nil
}
