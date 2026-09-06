// Package traffic generates exogenous arrival schedules for
// internal/replay.Scenario -- the PRD's Traffic Generator (§8.1),
// missing with no disclosure trail per the Stage 8 audit's F-06. It is
// deliberately a pure, standalone package: Generate returns a plain
// []replay.Arrival slice, the same type every hand-written scenario in
// this project has always built directly, so nothing about
// Scenario/RunWorld needs to change to consume it.
//
// Every pattern's arrival times are produced by inverse-transform
// sampling against that pattern's own closed-form cumulative-rate
// integral, not by rejection sampling or thinning -- given a target
// arrival count N and a shape, the i-th arrival lands at the exact
// normalized position (i+0.5)/N along that shape's cumulative-rate
// curve. This is deterministic by construction (same pattern+params+seed
// always produces the same unjittered grid) and guarantees exactly N
// arrivals land within [0, Horizon], which a thinning-based Poisson
// simulator would not guarantee without an outer retry loop.
package traffic

import (
	"fmt"
	"math"
	"math/rand"
	"time"

	"flashflow/internal/replay"
)

// Pattern selects which closed-form rate shape Generate samples from.
type Pattern string

const (
	Constant   Pattern = "constant"
	RampUp     Pattern = "ramp_up"
	RampDown   Pattern = "ramp_down"
	Burst      Pattern = "burst"
	FlashCrowd Pattern = "flash_crowd"
)

// Params configures one Generate call. Not every field is meaningful for
// every Pattern -- see each pattern's own doc comment below for which
// fields it reads.
type Params struct {
	Requests int           // total arrivals to produce -- exact, by construction
	Horizon  time.Duration // the window all arrivals fall within, [0, Horizon]

	BaseRate float64 // req/s -- see per-pattern meaning
	PeakRate float64 // req/s -- see per-pattern meaning

	BurstAt    time.Duration // Burst/FlashCrowd only: center of the spike
	BurstWidth time.Duration // Burst/FlashCrowd only: total width of the spike

	JitterFraction float64 // +/- fraction of the nominal (Horizon/Requests) inter-arrival gap, applied per arrival

	// KeyFunc assigns arrival i its cache key. Defaults to a single
	// constant key ("/") if nil -- callers that don't care about key
	// distribution get a working generator with zero configuration.
	KeyFunc func(i int) string
}

func (p Params) keyFor(i int) string {
	if p.KeyFunc == nil {
		return "/"
	}
	return p.KeyFunc(i)
}

// HotColdKeys returns a KeyFunc where a hotWeight fraction of arrivals
// (by simple deterministic round-robin count, not randomness -- keeping
// the whole generator seed-free except for JitterFraction) hit a single
// "/hot" key and the rest cycle across three "/cold-N" keys, mirroring
// the hot/cold rotation every hand-built tuning scenario in this project
// already uses (internal/tuning/scenario.go's keyForIndex).
func HotColdKeys(hotWeight float64) func(i int) string {
	if hotWeight < 0 {
		hotWeight = 0
	}
	if hotWeight > 1 {
		hotWeight = 1
	}
	return func(i int) string {
		// A deterministic low-discrepancy-ish selection: arrival i is
		// "hot" if its fractional position modulo 1 falls under
		// hotWeight, using i's own value scaled by hotWeight rather than
		// a counter that resets -- avoids clumping all hot keys at the
		// start when hotWeight is low.
		if math.Mod(float64(i)*hotWeight, 1.0) < hotWeight {
			return "/hot"
		}
		return fmt.Sprintf("/cold-%d", i%3)
	}
}

// Generate produces exactly p.Requests arrivals for pattern, seeded for
// reproducible jitter. Arrivals are not required to be time-sorted in
// the returned slice (internal/replay.RunWorld schedules each Arrival
// independently through vtime.Engine's own time-ordered event queue),
// matching every other Scenario generator in this project.
func Generate(pattern Pattern, p Params, seed int64) ([]replay.Arrival, error) {
	if p.Requests <= 0 {
		return nil, fmt.Errorf("traffic: Requests must be positive, got %d", p.Requests)
	}
	if p.Horizon <= 0 {
		return nil, fmt.Errorf("traffic: Horizon must be positive, got %v", p.Horizon)
	}

	var invert func(u float64) (float64, error) // normalized cumulative fraction -> normalized time x in [0,1]
	switch pattern {
	case Constant:
		invert = func(u float64) (float64, error) { return u, nil }
	case RampUp:
		if err := requirePositiveRates(p.BaseRate, p.PeakRate); err != nil {
			return nil, err
		}
		invert = func(u float64) (float64, error) { return rampInverse(p.BaseRate, p.PeakRate, u), nil }
	case RampDown:
		if err := requirePositiveRates(p.BaseRate, p.PeakRate); err != nil {
			return nil, err
		}
		invert = func(u float64) (float64, error) { return rampInverse(p.PeakRate, p.BaseRate, u), nil }
	case Burst:
		if err := requirePositiveRates(p.BaseRate, p.PeakRate); err != nil {
			return nil, err
		}
		inv, err := burstInverter(p)
		if err != nil {
			return nil, err
		}
		invert = inv
	case FlashCrowd:
		if err := requirePositiveRates(p.BaseRate, p.PeakRate); err != nil {
			return nil, err
		}
		inv, err := flashCrowdInverter(p)
		if err != nil {
			return nil, err
		}
		invert = inv
	default:
		return nil, fmt.Errorf("traffic: unknown pattern %q", pattern)
	}

	var rng *rand.Rand
	nominalGap := float64(p.Horizon) / float64(p.Requests)
	jitterRange := p.JitterFraction * nominalGap
	if jitterRange > 0 {
		rng = rand.New(rand.NewSource(seed))
	}

	arrivals := make([]replay.Arrival, p.Requests)
	for i := 0; i < p.Requests; i++ {
		u := (float64(i) + 0.5) / float64(p.Requests)
		x, err := invert(u)
		if err != nil {
			return nil, err
		}
		atNanos := x * float64(p.Horizon)
		if rng != nil {
			atNanos += (rng.Float64()*2 - 1) * jitterRange
		}
		if atNanos < 0 {
			atNanos = 0
		}
		if atNanos > float64(p.Horizon) {
			atNanos = float64(p.Horizon)
		}
		arrivals[i] = replay.Arrival{
			At:  0, // set below via VirtualTime conversion, kept explicit for clarity
			Key: p.keyFor(i),
		}
		arrivals[i].At = arrivals[i].At.Add(time.Duration(atNanos))
	}
	return arrivals, nil
}

func requirePositiveRates(base, peak float64) error {
	if base < 0 || peak < 0 {
		return fmt.Errorf("traffic: BaseRate and PeakRate must be non-negative, got %v and %v", base, peak)
	}
	if base == 0 && peak == 0 {
		return fmt.Errorf("traffic: BaseRate and PeakRate cannot both be zero")
	}
	return nil
}

// rampInverse solves for the normalized time x in [0,1] at which the
// cumulative area under a linear rate curve from r0 (at x=0) to r1 (at
// x=1) reaches u * totalArea, where totalArea = (r0+r1)/2 (the trapezoid
// rule applied to a single linear segment -- exact, not an
// approximation). This is the shared closed-form inversion RampUp
// (r0=BaseRate, r1=PeakRate) and RampDown (r0=PeakRate, r1=BaseRate) both
// reduce to.
//
// Derivation: rate(x) = r0 + (r1-r0)*x. Cumulative A(x) = r0*x +
// (r1-r0)*x^2/2. Setting A(x) = target and solving the quadratic
// (r1-r0)/2 * x^2 + r0*x - target = 0 for x gives the formula below; the
// "+" root is the one that lands in [0,1] for target in [0, totalArea]
// (verified: at target=0, x=0; at target=totalArea, x=1 -- see
// generator_test.go's TestRampInverse_Endpoints).
func rampInverse(r0, r1, u float64) float64 {
	totalArea := (r0 + r1) / 2
	target := u * totalArea
	a := (r1 - r0) / 2
	if a == 0 {
		// Constant rate (r0 == r1 == totalArea): degenerates to uniform
		// spacing, x = target/r0.
		if r0 == 0 {
			return u // both zero was already rejected by requirePositiveRates; defensive only
		}
		return target / r0
	}
	// a*x^2 + r0*x - target = 0
	disc := r0*r0 + 4*a*target
	if disc < 0 {
		disc = 0 // guards float rounding pushing a near-zero discriminant slightly negative
	}
	x := (-r0 + math.Sqrt(disc)) / (2 * a)
	if x < 0 {
		x = 0
	}
	if x > 1 {
		x = 1
	}
	return x
}

// burstInverter builds the exact inverse-CDF for a rectangular spike:
// BaseRate everywhere except PeakRate on [burstStartX, burstEndX)
// (BurstAt +/- BurstWidth/2, clamped into [0,1] and to a non-empty
// window). Three piecewise-CONSTANT segments -- each one's cumulative
// contribution is a plain multiplication, no quadratic needed, unlike
// the linear-ramp patterns.
func burstInverter(p Params) (func(u float64) (float64, error), error) {
	if p.Horizon <= 0 {
		return nil, fmt.Errorf("traffic: Horizon must be positive")
	}
	startX := clamp01(float64(p.BurstAt-p.BurstWidth/2) / float64(p.Horizon))
	endX := clamp01(float64(p.BurstAt+p.BurstWidth/2) / float64(p.Horizon))
	if endX < startX {
		startX, endX = endX, startX
	}

	preArea := p.BaseRate * startX
	burstArea := p.PeakRate * (endX - startX)
	postArea := p.BaseRate * (1 - endX)
	total := preArea + burstArea + postArea
	if total <= 0 {
		return nil, fmt.Errorf("traffic: burst profile has zero total rate")
	}

	return func(u float64) (float64, error) {
		target := u * total
		switch {
		case target <= preArea:
			if p.BaseRate == 0 {
				return 0, nil
			}
			return target / p.BaseRate, nil
		case target <= preArea+burstArea:
			if p.PeakRate == 0 {
				return startX, nil
			}
			return startX + (target-preArea)/p.PeakRate, nil
		default:
			if p.BaseRate == 0 {
				return endX, nil
			}
			return endX + (target-preArea-burstArea)/p.BaseRate, nil
		}
	}, nil
}

// flashCrowdInverter models a real flash crowd's characteristic
// asymmetry -- a fast onset, slower decay -- as four piecewise segments
// over normalized time: constant at BaseRate, a short linear rise to
// PeakRate, a longer linear decay back to BaseRate, constant at BaseRate
// again. The rise gets 1/4 of BurstWidth and the decay gets 3/4, a
// deliberate, documented asymmetry (not a tunable parameter -- this
// project's own scope is "one flash-crowd shape exists," not a general
// asymmetric-pulse builder) distinguishing it from Burst's symmetric
// rectangle.
func flashCrowdInverter(p Params) (func(u float64) (float64, error), error) {
	if p.Horizon <= 0 {
		return nil, fmt.Errorf("traffic: Horizon must be positive")
	}
	riseWidth := p.BurstWidth / 4
	decayWidth := p.BurstWidth - riseWidth
	riseStartX := clamp01(float64(p.BurstAt-riseWidth) / float64(p.Horizon))
	peakX := clamp01(float64(p.BurstAt) / float64(p.Horizon))
	decayEndX := clamp01(float64(p.BurstAt+decayWidth) / float64(p.Horizon))
	if !(riseStartX <= peakX && peakX <= decayEndX) {
		return nil, fmt.Errorf("traffic: flash-crowd window (BurstAt=%v, BurstWidth=%v) does not fit within Horizon=%v",
			p.BurstAt, p.BurstWidth, p.Horizon)
	}

	preArea := p.BaseRate * riseStartX
	riseArea := (p.BaseRate + p.PeakRate) / 2 * (peakX - riseStartX)
	decayArea := (p.PeakRate + p.BaseRate) / 2 * (decayEndX - peakX)
	postArea := p.BaseRate * (1 - decayEndX)
	total := preArea + riseArea + decayArea + postArea
	if total <= 0 {
		return nil, fmt.Errorf("traffic: flash-crowd profile has zero total rate")
	}

	return func(u float64) (float64, error) {
		target := u * total
		switch {
		case target <= preArea:
			if p.BaseRate == 0 {
				return 0, nil
			}
			return target / p.BaseRate, nil
		case target <= preArea+riseArea:
			segWidth := peakX - riseStartX
			if segWidth <= 0 {
				return riseStartX, nil
			}
			localU := (target - preArea) / riseArea
			return riseStartX + rampInverse(p.BaseRate, p.PeakRate, localU)*segWidth, nil
		case target <= preArea+riseArea+decayArea:
			segWidth := decayEndX - peakX
			if segWidth <= 0 {
				return peakX, nil
			}
			localU := (target - preArea - riseArea) / decayArea
			return peakX + rampInverse(p.PeakRate, p.BaseRate, localU)*segWidth, nil
		default:
			if p.BaseRate == 0 {
				return decayEndX, nil
			}
			return decayEndX + (target-preArea-riseArea-decayArea)/p.BaseRate, nil
		}
	}, nil
}

func clamp01(x float64) float64 {
	if x < 0 {
		return 0
	}
	if x > 1 {
		return 1
	}
	return x
}

// ScheduleReal dispatches arrivals against wall-clock time, each on its
// own independent absolute-time sleep rather than a shared time.Ticker
// -- the exact open-loop pattern cmd/experiment-008g's own doc comment
// explains and confirmed avoids coordinated omission (a shared ticker
// silently drops ticks under load, throttling the generator itself
// rather than the system under test). dispatch is called synchronously
// on its own goroutine per arrival; callers needing to wait for all
// dispatches to fire should use their own sync.WaitGroup around
// dispatch, matching how every real-engine experiment in this project
// already structures its own dispatch loop.
func ScheduleReal(arrivals []replay.Arrival, start time.Time, dispatch func(key string)) {
	for _, a := range arrivals {
		target := start.Add(time.Duration(a.At.Nanoseconds()))
		go func(key string, target time.Time) {
			if d := time.Until(target); d > 0 {
				time.Sleep(d)
			}
			dispatch(key)
		}(a.Key, target)
	}
}
