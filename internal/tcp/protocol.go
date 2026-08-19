package tcp

import (
	"encoding/binary"
	"fmt"
	"io"
)

const (
	// MaxMessageSize is the largest payload accepted by ReadMessage.
	// Keeping a hard limit prevents a malicious or corrupted length prefix
	// from causing an unexpectedly large memory allocation.
	MaxMessageSize = 10 * 1024 * 1024 // 10 MiB

	// messageHeaderSize is the size of the length prefix in bytes.
	messageHeaderSize = 4
)

// ReadMessage reads exactly one length-prefixed message from r.
// The wire format is:[length: uint32 big-endian][payload: length bytes]
// TCP is a byte stream rather than a message-oriented protocol. A single
// message may therefore arrive across multiple Read calls, or multiple
// messages may arrive in a single Read call. io.ReadFull is used to ensure
// that the complete header and payload are consumed before returning.
// If the connection closes before any bytes of the header are read,
// io.EOF is returned. If the connection closes after the header has been
// received but before the complete payload arrives, io.ErrUnexpectedEOF
// (wrapped below) is returned.

func ReadMessage(r io.Reader) ([]byte, error) {
	var length uint32

	// binary.Read reads the complete four-byte length prefix from r.
	// It may internally perform multiple reads because r can be a TCP stream.

	if err := binary.Read(r, binary.BigEndian, &length); err != nil {
		return nil, fmt.Errorf("read message length: %w", err)
	}

	// Reject oversized frames before allocating memory for the payload.
	if length > MaxMessageSize {
		return nil, fmt.Errorf(
			"message too large: %d bytes (maximum %d)",
			length,
			MaxMessageSize,
		)
	}

	// A zero-length payload is a valid message and results in a non-nil,
	// zero-length slice.
	payload := make([]byte, length)

	// io.ReadFull is important here: a single Read is not guaranteed to
	// return the entire payload, even when the peer sent the complete frame.
	if _, err := io.ReadFull(r, payload); err != nil {
		return nil, fmt.Errorf("read message payload (%d bytes): %w", length, err)
	}

	return payload, nil
}

// WriteMessage writes payload as one length-prefixed frame to w.
// The wire format is: [length: uint32 big-endian][payload: length bytes]
// The complete frame is assembled into one buffer so the caller makes one
// application-level Write operation per frame. This does NOT mean TCP sends
// the frame as one packet; TCP may split or coalesce bytes arbitrarily.
// The receiver must therefore always use ReadMessage-style framing.
// WriteMessage handles short writes by continuing until the entire frame has
// been written or an error is returned.
func WriteMessage(w io.Writer, payload []byte) error {
	if uint64(len(payload)) > uint64(MaxMessageSize) {
		return fmt.Errorf(
			"message too large: %d bytes (maximum %d)",
			len(payload),
			MaxMessageSize,
		)
	}

	// The protocol uses a uint32 length prefix, so explicitly verify that
	// the payload can be represented by uint32 before converting.
	if uint64(len(payload)) > uint64(^uint32(0)) {
		return fmt.Errorf("message too large for uint32 length prefix: %d bytes", len(payload))
	}

	buf := make([]byte, messageHeaderSize+len(payload))
	binary.BigEndian.PutUint32(buf[:messageHeaderSize], uint32(len(payload)))
	copy(buf[messageHeaderSize:], payload)

	// io.Writer implementations are allowed to perform a short write.
	// Continue writing until the complete frame has been consumed.
	for len(buf) > 0 {
		n, err := w.Write(buf)
		if err != nil {
			return fmt.Errorf("write message: %w", err)
		}

		// A broken Writer is allowed to violate the io.Writer contract by
		// returning an invalid byte count. Avoid a possible infinite loop
		// if that happens.
		if n <= 0 {
			return io.ErrShortWrite
		}

		buf = buf[n:]
	}

	return nil
}