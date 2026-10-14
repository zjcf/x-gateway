package webfill

import (
	"encoding/binary"
	"encoding/json"
	"io"
	"x-gateway/internal/model"
)

const maxMessage = 1 << 20

func readFrame(r io.Reader) ([]byte, error) {
	var h [4]byte
	if _, e := io.ReadFull(r, h[:]); e != nil {
		return nil, e
	}
	n := binary.LittleEndian.Uint32(h[:])
	if n == 0 || n > maxMessage {
		return nil, model.Err("PROTOCOL_ERROR", "消息长度无效")
	}
	b := make([]byte, n)
	_, e := io.ReadFull(r, b)
	if e == nil && !json.Valid(b) {
		e = model.Err("PROTOCOL_ERROR", "消息JSON无效")
	}
	return b, e
}
func writeFrame(w io.Writer, b []byte) error {
	if len(b) == 0 || len(b) > maxMessage || !json.Valid(b) {
		return model.Err("PROTOCOL_ERROR", "消息JSON无效")
	}
	var h [4]byte
	binary.LittleEndian.PutUint32(h[:], uint32(len(b)))
	for _, part := range [][]byte{h[:], b} {
		for len(part) > 0 {
			n, e := w.Write(part)
			if e != nil {
				return e
			}
			if n == 0 {
				return io.ErrShortWrite
			}
			part = part[n:]
		}
	}
	return nil
}
