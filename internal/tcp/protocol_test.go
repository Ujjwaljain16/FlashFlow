package tcp

import (
	"bytes"
	"io"
	"reflect"
	"testing"
)

// slowReader wraps a bytes.Buffer and reads at most `chunkSize` bytes at a time
// to simulate partial network reads (fragmentation).
type slowReader struct {
	buf       *bytes.Buffer
	chunkSize int
}

func (s *slowReader) Read(p []byte) (n int, err error) {
	if len(p) > s.chunkSize {
		p = p[:s.chunkSize]
	}
	return s.buf.Read(p)
}

func TestProtocol_WriteAndRead(t *testing.T) {
	tests := []struct {
		name    string
		payload []byte
	}{
		{"empty payload", []byte{}},
		{"short message", []byte("hello")},
		{"large message", bytes.Repeat([]byte("A"), 10000)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			err := WriteMessage(&buf, tt.payload)
			if err != nil {
				t.Fatalf("WriteMessage failed: %v", err)
			}

			readPayload, err := ReadMessage(&buf)
			if err != nil {
				t.Fatalf("ReadMessage failed: %v", err)
			}

			if !reflect.DeepEqual(readPayload, tt.payload) {
				t.Errorf("expected %v, got %v", tt.payload, readPayload)
			}
		})
	}
}

func TestProtocol_PartialReads(t *testing.T) {
	payload := []byte("this is a test message for partial reads")
	var fullBuf bytes.Buffer
	WriteMessage(&fullBuf, payload)

	// Wrap in a slowReader that only yields 3 bytes at a time
	sr := &slowReader{
		buf:       bytes.NewBuffer(fullBuf.Bytes()),
		chunkSize: 3,
	}

	readPayload, err := ReadMessage(sr)
	if err != nil {
		t.Fatalf("ReadMessage failed: %v", err)
	}

	if string(readPayload) != string(payload) {
		t.Errorf("expected %q, got %q", payload, readPayload)
	}
}

func TestProtocol_MultipleMessages(t *testing.T) {
	messages := [][]byte{
		[]byte("msg1"),
		[]byte("message 2"),
		[]byte("3"),
	}

	var buf bytes.Buffer
	for _, msg := range messages {
		WriteMessage(&buf, msg)
	}

	for i, expected := range messages {
		readMsg, err := ReadMessage(&buf)
		if err != nil {
			t.Fatalf("failed to read message %d: %v", i, err)
		}
		if string(readMsg) != string(expected) {
			t.Errorf("expected %q, got %q", expected, readMsg)
		}
	}

	// Next read should be EOF
	_, err := ReadMessage(&buf)
	if err != io.EOF {
		t.Errorf("expected EOF, got %v", err)
	}
}
