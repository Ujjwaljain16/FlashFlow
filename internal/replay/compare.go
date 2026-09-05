package replay

import (
	"reflect"

	"flashflow/internal/vtime"
)

// FirstDivergence returns the index of the first TraceEvent at which a
// and b differ, and true. If one trace is a prefix of the other, the
// shorter trace's length is returned. If the traces are identical, it
// returns (0, false).
//
// This exists specifically for divergence tests: two Scenarios built to
// share identical history up to some cutoff time should produce traces
// that are byte-for-byte equal up to that point and differ starting
// exactly there -- not earlier (which would mean a later change is
// somehow visible in the past, a causality bug) and not "eventually,
// somewhere" (which would be too weak a claim to trust).
func FirstDivergence(a, b []vtime.TraceEvent) (int, bool) {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if !reflect.DeepEqual(a[i], b[i]) {
			return i, true
		}
	}
	if len(a) != len(b) {
		return n, true
	}
	return 0, false
}
