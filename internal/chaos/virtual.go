package chaos

import (
	"fmt"
	"sort"
	"time"

	"flashflow/internal/clock"
	"flashflow/internal/replay"
)

// ToFailureWindows compiles s into replay.FailureWindows for the
// virtual engine, pairing each target's crash with its matching
// recover in time order. It errors rather than silently dropping
// anything it cannot express:
//   - a "latency" action, since replay.Scenario has no way to change a
//     target's ServiceTime mid-run (a disclosed, deliberate asymmetry --
//     see this package's own doc comment and Stage 10's exit artifact);
//   - a crash with no matching recover, or a recover with no matching
//     crash, for the same target;
//   - two crashes for the same target with no recover between them.
//
// Events are sorted by At before pairing, since a Schedule's parse
// order is not guaranteed to already be time-ordered.
func (s Schedule) ToFailureWindows() ([]replay.FailureWindow, error) {
	sorted := make(Schedule, len(s))
	copy(sorted, s)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].At < sorted[j].At })

	openCrash := make(map[string]time.Duration)
	var windows []replay.FailureWindow

	for _, ev := range sorted {
		switch ev.Action {
		case Latency:
			return nil, fmt.Errorf("chaos: ToFailureWindows cannot express a %q action for target %q -- "+
				"the virtual engine has no way to change a target's ServiceTime mid-run (a disclosed, "+
				"deliberate asymmetry between the two engines)", Latency, ev.Target)
		case Crash:
			if _, alreadyDown := openCrash[ev.Target]; alreadyDown {
				return nil, fmt.Errorf("chaos: target %q crashes again at %v before its previous crash was recovered", ev.Target, ev.At)
			}
			openCrash[ev.Target] = ev.At
		case Recover:
			downAt, wasDown := openCrash[ev.Target]
			if !wasDown {
				return nil, fmt.Errorf("chaos: target %q recovers at %v with no matching crash", ev.Target, ev.At)
			}
			windows = append(windows, replay.FailureWindow{
				Target: ev.Target,
				DownAt: clock.VirtualTime(downAt.Nanoseconds()),
				UpAt:   clock.VirtualTime(ev.At.Nanoseconds()),
			})
			delete(openCrash, ev.Target)
		}
	}

	if len(openCrash) > 0 {
		unrecovered := make([]string, 0, len(openCrash))
		for target := range openCrash {
			unrecovered = append(unrecovered, target)
		}
		sort.Strings(unrecovered) // deterministic error message regardless of map iteration order
		return nil, fmt.Errorf("chaos: target(s) %v crash with no matching recover", unrecovered)
	}

	return windows, nil
}
