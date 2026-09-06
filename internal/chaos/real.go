package chaos

import (
	"fmt"
	"time"

	"flashflow/internal/topology"
)

// ScheduledAction is one Event compiled down to a concrete, no-argument
// action against a real EdgeServer -- Run is always one of
// EdgeServer.SetDown or EdgeServer.SetArtificialDelay, never a new,
// third injection mechanism.
type ScheduledAction struct {
	At     time.Duration
	Target string
	Run    func()
}

// ToRealSchedule compiles s into ScheduledActions against edges (keyed
// by instance name, e.g. from EdgeServer.Instance()). It errors if an
// event names a target not present in edges, rather than silently
// skipping that event -- a chaos schedule that silently drops part of
// itself is worse than useless, since a reader trusting the schedule
// file would believe an injection happened that never did.
//
// crash/recover compile to SetDown(true)/SetDown(false); latency
// compiles to SetArtificialDelay(Delay) -- no netsim.Conditions
// mutation is needed or attempted, since a chaos schedule's "latency"
// action models application-level processing delay, the same knob
// DefaultDelay/SetArtificialDelay already control, not network-level
// impairment (that remains internal/netsim's own, separately-configured
// concern).
func (s Schedule) ToRealSchedule(edges map[string]*topology.EdgeServer) ([]ScheduledAction, error) {
	actions := make([]ScheduledAction, 0, len(s))
	for _, ev := range s {
		edge, ok := edges[ev.Target]
		if !ok {
			return nil, fmt.Errorf("chaos: event targets %q, but no EdgeServer with that instance name was provided", ev.Target)
		}
		// No per-iteration variable shadowing needed here: Go 1.22+
		// (this project targets 1.23) already gives `for range` its own
		// fresh ev/edge binding each iteration, so the closures below
		// each capture their own iteration's values correctly.
		var run func()
		switch ev.Action {
		case Crash:
			run = func() { edge.SetDown(true) }
		case Recover:
			run = func() { edge.SetDown(false) }
		case Latency:
			run = func() { edge.SetArtificialDelay(ev.Delay) }
		default:
			return nil, fmt.Errorf("chaos: unknown action %q for target %q", ev.Action, ev.Target)
		}
		actions = append(actions, ScheduledAction{At: ev.At, Target: ev.Target, Run: run})
	}
	return actions, nil
}

// RunReal dispatches actions against wall-clock time, each on its own
// independent absolute-time sleep -- the same open-loop pattern
// internal/traffic.ScheduleReal and cmd/experiment-008g's own dispatch
// loop already use, and for the identical reason: a shared time.Ticker
// silently drops ticks under load, throttling the dispatcher itself
// rather than accurately timing the scheduled actions. Fire-and-forget:
// callers needing to know when every action has fired should wrap this
// with their own synchronization, the same convention ScheduleReal
// already documents.
func RunReal(actions []ScheduledAction, start time.Time) {
	for _, a := range actions {
		target := start.Add(a.At)
		go func(run func(), target time.Time) {
			if d := time.Until(target); d > 0 {
				time.Sleep(d)
			}
			run()
		}(a.Run, target)
	}
}
