package cache

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestCoalescer_SingleCallerActsAsLeader(t *testing.T) {
	c := NewCoalescer()
	entry, err, shared := c.Do("k", func() (Entry, error) {
		return Entry{StatusCode: 200}, nil
	})
	if err != nil || shared || entry.StatusCode != 200 {
		t.Fatalf("got entry=%+v err=%v shared=%v", entry, err, shared)
	}
	stats := c.Snapshot()
	if stats.Leads != 1 || stats.Shared != 0 {
		t.Fatalf("expected 1 lead, 0 shared, got %+v", stats)
	}
}

// TestCoalescer_ConcurrentCallsShareOneFetch proves the central promise of
// coalescing: N concurrent callers for the same key produce exactly one
// fn invocation. release blocks fn until every goroutine is provably
// waiting on Do, so this can't pass by accident (fn racing ahead of the
// other goroutines even reaching Do at all).
func TestCoalescer_ConcurrentCallsShareOneFetch(t *testing.T) {
	c := NewCoalescer()
	const n = 50

	var calls atomic.Int64
	var entered sync.WaitGroup
	entered.Add(1)
	release := make(chan struct{})

	fn := func() (Entry, error) {
		calls.Add(1)
		entered.Done()
		<-release
		return Entry{StatusCode: 200, Body: []byte("shared")}, nil
	}

	var wg sync.WaitGroup
	results := make([]Entry, n)
	shares := make([]bool, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			entry, err, shared := c.Do("hot", fn)
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			results[i] = entry
			shares[i] = shared
		}(i)
	}

	entered.Wait()                    // the leader has started fn and is now blocked on release
	time.Sleep(20 * time.Millisecond) // give the other n-1 goroutines time to queue up as waiters
	close(release)
	wg.Wait()

	if got := calls.Load(); got != 1 {
		t.Fatalf("expected fn to run exactly once, ran %d times", got)
	}
	leaders := 0
	for i, s := range shares {
		if string(results[i].Body) != "shared" {
			t.Fatalf("result %d did not receive the leader's value: %+v", i, results[i])
		}
		if !s {
			leaders++
		}
	}
	if leaders != 1 {
		t.Fatalf("expected exactly one non-shared (leader) call, got %d", leaders)
	}

	stats := c.Snapshot()
	if stats.Leads != 1 || stats.Shared != n-1 {
		t.Fatalf("expected 1 lead and %d shared, got %+v", n-1, stats)
	}
}

func TestCoalescer_DifferentKeysDoNotCoalesce(t *testing.T) {
	c := NewCoalescer()
	var calls atomic.Int64
	fn := func() (Entry, error) {
		calls.Add(1)
		return Entry{}, nil
	}

	var wg sync.WaitGroup
	for _, key := range []string{"a", "b"} {
		wg.Add(1)
		go func(key string) {
			defer wg.Done()
			c.Do(key, fn)
		}(key)
	}
	wg.Wait()

	if got := calls.Load(); got != 2 {
		t.Fatalf("expected one fetch per distinct key (2 total), got %d", got)
	}
}

func TestCoalescer_FailurePropagatesToAllWaiters(t *testing.T) {
	c := NewCoalescer()
	wantErr := errors.New("upstream unreachable")

	entered := make(chan struct{})
	release := make(chan struct{})
	fn := func() (Entry, error) {
		close(entered)
		<-release
		return Entry{}, wantErr
	}

	const n = 10
	var wg sync.WaitGroup
	errs := make([]error, n)
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, errs[0], _ = c.Do("k", fn)
	}()
	<-entered

	for i := 1; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i], _ = c.Do("k", func() (Entry, error) {
				t.Error("waiter must not run fn itself")
				return Entry{}, nil
			})
		}(i)
	}
	time.Sleep(20 * time.Millisecond)
	close(release)
	wg.Wait()

	for i, err := range errs {
		if !errors.Is(err, wantErr) {
			t.Fatalf("caller %d got err=%v, want %v", i, err, wantErr)
		}
	}

	stats := c.Snapshot()
	if stats.Failures != n {
		t.Fatalf("expected all %d calls counted as failures, got %+v", n, stats)
	}
}

// TestCoalescer_NoAbandonedEntryAfterCompletion is the master-context
// invariant test: once a fetch finishes, the key must be free for a
// brand new leader immediately -- not stuck waiting on a stale entry.
func TestCoalescer_NoAbandonedEntryAfterCompletion(t *testing.T) {
	c := NewCoalescer()
	var calls atomic.Int64
	fn := func() (Entry, error) {
		calls.Add(1)
		return Entry{}, nil
	}

	c.Do("k", fn)
	c.Do("k", fn)

	if got := calls.Load(); got != 2 {
		t.Fatalf("expected a fresh fn call after the first completed, got %d total calls", got)
	}
}

func TestCoalescer_NoAbandonedEntryAfterFailure(t *testing.T) {
	c := NewCoalescer()
	var calls atomic.Int64
	failThenSucceed := func() (Entry, error) {
		n := calls.Add(1)
		if n == 1 {
			return Entry{}, errors.New("boom")
		}
		return Entry{StatusCode: 200}, nil
	}

	_, err1, _ := c.Do("k", failThenSucceed)
	if err1 == nil {
		t.Fatal("expected first call to fail")
	}
	entry2, err2, _ := c.Do("k", failThenSucceed)
	if err2 != nil || entry2.StatusCode != 200 {
		t.Fatalf("expected a fresh, successful call after the failure, got entry=%+v err=%v", entry2, err2)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("expected exactly 2 fn calls, got %d", got)
	}
}

func TestCoalescer_PanicIsRecoveredAndSharedAsError(t *testing.T) {
	c := NewCoalescer()

	_, err, _ := c.Do("k", func() (Entry, error) {
		panic("upstream client blew up")
	})
	if err == nil {
		t.Fatal("expected panic to be converted into an error, got nil")
	}

	// the key must not be poisoned -- a later call for the same key works normally.
	entry, err2, _ := c.Do("k", func() (Entry, error) {
		return Entry{StatusCode: 200}, nil
	})
	if err2 != nil || entry.StatusCode != 200 {
		t.Fatalf("expected key to be usable again after a panic, got entry=%+v err=%v", entry, err2)
	}
}
