package tcp

import (
	"sync/atomic"
)

// Tracker safely maintains metrics for the TCP server using atomic operations.
//
// Invariants that must hold after a clean run:
//   - ActiveConns == TotalAccepted - TotalClosed
//   - MessagesSent <= MessagesRecv  (echo server; server can fail before sending)
//   - BytesSent == 4*MessagesSent + sum(payload bytes sent)
type Tracker struct {
	ActiveConns   int64
	TotalAccepted int64
	TotalClosed   int64
	MessagesRecv  int64
	MessagesSent  int64
	Errors        int64
	BytesRecv     int64
	BytesSent     int64
}

func (t *Tracker) IncAccepted() {
	atomic.AddInt64(&t.TotalAccepted, 1)
	atomic.AddInt64(&t.ActiveConns, 1)
}

func (t *Tracker) IncClosed() {
	atomic.AddInt64(&t.TotalClosed, 1)
	atomic.AddInt64(&t.ActiveConns, -1)
}

func (t *Tracker) AddBytesRecv(n int64) { atomic.AddInt64(&t.BytesRecv, n) }
func (t *Tracker) AddBytesSent(n int64) { atomic.AddInt64(&t.BytesSent, n) }
func (t *Tracker) IncMsgRecv()          { atomic.AddInt64(&t.MessagesRecv, 1) }
func (t *Tracker) IncMsgSent()          { atomic.AddInt64(&t.MessagesSent, 1) }
func (t *Tracker) IncError()            { atomic.AddInt64(&t.Errors, 1) }

// Snapshot returns a consistent point-in-time copy of the current metrics.
// Individual fields are read atomically but the snapshot is not globally atomic.
func (t *Tracker) Snapshot() Tracker {
	return Tracker{
		ActiveConns:   atomic.LoadInt64(&t.ActiveConns),
		TotalAccepted: atomic.LoadInt64(&t.TotalAccepted),
		TotalClosed:   atomic.LoadInt64(&t.TotalClosed),
		MessagesRecv:  atomic.LoadInt64(&t.MessagesRecv),
		MessagesSent:  atomic.LoadInt64(&t.MessagesSent),
		Errors:        atomic.LoadInt64(&t.Errors),
		BytesRecv:     atomic.LoadInt64(&t.BytesRecv),
		BytesSent:     atomic.LoadInt64(&t.BytesSent),
	}
}
