package engine

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"flashflow/internal/chaos"
	"flashflow/internal/clock"
	"flashflow/internal/health"
	"flashflow/internal/proxy"
	"flashflow/internal/replay"
	"flashflow/internal/telemetry"
	"flashflow/internal/topology"
	"flashflow/internal/traffic"
	"flashflow/internal/transport"
)

// RealEngine implements ExperimentEngine over the real HTTP stack:
// one topology.OriginServer, one topology.EdgeServer per configured
// edge, and one proxy.ReverseProxy routing across them using the
// SAME replay.PolicySpec the virtual engine uses -- this project's own
// "same policy code runs in both engines" property, now exercised
// through one shared front door rather than only by hand in each
// cmd/experiment-* binary.
type RealEngine struct{}

// NewRealEngine constructs a RealEngine. Stateless: every Run/Replay
// call starts and tears down its own Origin/Edges/Proxy, never reusing
// state across calls.
func NewRealEngine() RealEngine {
	return RealEngine{}
}

// Prepare rejects an Experiment the real engine cannot execute: a nil
// Real config, zero configured edges, or a non-positive traffic
// Horizon (which would mean "run forever," never a real experiment's
// intent).
func (RealEngine) Prepare(exp Experiment) error {
	if exp.Real == nil {
		return fmt.Errorf("engine: experiment %q has no RealExperimentConfig", exp.ID)
	}
	if len(exp.Real.Edges) == 0 {
		return fmt.Errorf("engine: experiment %q has no real edges configured", exp.ID)
	}
	if exp.Real.TrafficParams.Horizon <= 0 {
		return fmt.Errorf("engine: experiment %q real traffic params need a positive Horizon", exp.ID)
	}
	if exp.Policy.New == nil {
		return fmt.Errorf("engine: experiment %q has no policy constructor", exp.ID)
	}
	if err := ValidateConsistency(exp); err != nil {
		return err
	}
	return nil
}

// Run executes exp.Policy against exp.Real's real edges.
func (r RealEngine) Run(exp Experiment) (RunResult, error) {
	return r.run(exp, exp.Policy)
}

// Replay executes policy (instead of exp.Policy) against the identical
// real configuration -- the real-engine counterfactual: same Origin
// delay, same edges, same traffic pattern and chaos schedule, a
// different routing decision-maker. Each call starts an entirely fresh
// Origin/Edges/Proxy (see run's own doc comment), so two Replay calls
// never share real process state, matching the virtual engine's own
// "nothing shared between calls" guarantee as closely as two real
// listening HTTP servers on ephemeral ports can.
func (r RealEngine) Replay(exp Experiment, policy replay.PolicySpec) (RunResult, error) {
	return r.run(exp, policy)
}

func (r RealEngine) run(exp Experiment, policy replay.PolicySpec) (RunResult, error) {
	if err := r.Prepare(exp); err != nil {
		return RunResult{}, err
	}
	if policy.New == nil {
		return RunResult{}, fmt.Errorf("engine: real run policy for %q has no constructor", exp.ID)
	}
	cfg := exp.Real

	origin := topology.NewOriginServer(topology.OriginConfig{Instance: exp.ID + "-origin", DefaultDelay: cfg.OriginDelay})
	if err := origin.Start(); err != nil {
		return RunResult{}, fmt.Errorf("engine: starting origin for %q: %w", exp.ID, err)
	}
	defer origin.Stop(context.Background())

	edges := make(map[string]*topology.EdgeServer, len(cfg.Edges))
	var targets []replay.TargetProfile
	for name, delay := range cfg.Edges {
		e, err := topology.NewEdgeServer(topology.EdgeConfig{Instance: name, OriginURL: origin.URL(), DefaultDelay: delay})
		if err != nil {
			return RunResult{}, fmt.Errorf("engine: creating edge %q for %q: %w", name, exp.ID, err)
		}
		if err := e.Start(); err != nil {
			return RunResult{}, fmt.Errorf("engine: starting edge %q for %q: %w", name, exp.ID, err)
		}
		defer e.Stop(context.Background())
		edges[name] = e
		// TargetProfile.ServiceTime here is the real-engine analog of a
		// virtual target's fixed service time -- fed to policy.New so
		// WeightedRoundRobinPolicy's real-engine use gets the same
		// per-target capacity information its virtual-engine counterpart
		// already reads from Scenario.Targets.
		targets = append(targets, replay.TargetProfile{Name: e.URL(), ServiceTime: delay})
	}

	clk := clock.NewWallClock()

	targetURLs := make([]string, len(targets))
	for i, t := range targets {
		targetURLs[i] = t.Name
	}
	// Constructed with a nil selector first (NewReverseProxy defaults to
	// round-robin when sel is nil) specifically so the real selector can be
	// built AFTER the proxy exists, using the proxy's OWN LoadTracker/
	// LatencyTracker (pxy.LoadTracker()/LatencyTracker(), already exposed
	// for exactly this purpose) rather than a disconnected pair policy.New
	// would otherwise construct for itself. This is the Stage 12 Track A
	// fix for a real, confirmed defect (Stage 11 Program F,
	// docs/StageArtifacts/Stage11.md §10; the interim Stage 11 fix bridged
	// only the latency signal via a post-hoc X-Selected-Edge header read,
	// explicitly disclosed there as not restoring genuine concurrent load
	// tracking). proxy.ServeHTTP already increments p.loadTracker at the
	// real dispatch moment and decrements it at the real completion moment
	// (proxy.go lines 186-187), and observes real latency at line 300 --
	// sharing those exact objects with the selector means every dynamic
	// policy (least-connections, ewma, p2c-load, adaptive) now sees
	// genuine, concurrency-correct signals, not an approximation.
	// MaxConnsPerHost is capped PER EDGE HOST here, not globally -- Go's
	// http.Transport.MaxConnsPerHost is scoped per (scheme, host, port),
	// and each edge listens on its own ephemeral port, so setting this
	// once on the shared transport genuinely limits concurrency to each
	// individual edge independently (Stage 14 Track D).
	tcfg := transport.DefaultTransportConfig(exp.ID + "_proxy")
	if cfg.MaxConnsPerHost > 0 {
		tcfg.MaxConnsPerHost = cfg.MaxConnsPerHost
	}
	pxy := proxy.NewReverseProxy(proxy.Config{
		Targets:         targetURLs,
		TransportConfig: tcfg,
		HealthConfig:    health.DefaultConfig(),
		ProberConfig:    health.DefaultCheckerConfig(),
	}, clk, nil)

	selector, _ := policy.New(clk, exp.Scenario.Seeds, targets, replay.Trackers{
		Load:    pxy.LoadTracker(),
		Latency: pxy.LatencyTracker(),
	})
	pxy.SetSelector(selector)

	if err := pxy.Start(); err != nil {
		return RunResult{}, fmt.Errorf("engine: starting proxy for %q: %w", exp.ID, err)
	}
	defer pxy.Stop(context.Background())

	hist := telemetry.AttachHistogram(pxy)

	if len(cfg.Chaos) > 0 {
		actions, err := cfg.Chaos.ToRealSchedule(edges)
		if err != nil {
			return RunResult{}, fmt.Errorf("engine: compiling chaos schedule for %q: %w", exp.ID, err)
		}
		chaos.RunReal(actions, time.Now())
	}

	arrivals, err := traffic.Generate(cfg.TrafficPattern, cfg.TrafficParams, exp.Scenario.Seeds.Traffic)
	if err != nil {
		return RunResult{}, fmt.Errorf("engine: generating traffic for %q: %w", exp.ID, err)
	}

	client := &http.Client{Timeout: 5 * time.Second}
	var completed int
	var mu sync.Mutex
	var wg sync.WaitGroup
	wg.Add(len(arrivals))
	dispatch := func(key string) {
		defer wg.Done()
		// No manual tracker bridging needed here: the selector shares the
		// proxy's own trackers (constructed above), which proxy.ServeHTTP
		// itself increments/decrements/observes at the real dispatch and
		// completion boundaries, inside the request's actual lifetime --
		// genuine concurrent in-flight tracking, not a post-hoc
		// approximation computed after the response already returned.
		resp, err := client.Get(pxy.URL() + key)
		if err != nil {
			return
		}
		defer resp.Body.Close()
		mu.Lock()
		completed++
		mu.Unlock()
	}
	traffic.ScheduleReal(arrivals, time.Now(), dispatch)
	wg.Wait()

	m := telemetry.SnapshotFromProxy(pxy)
	m.Histogram = hist

	return RunResult{Engine: "real", Real: &RealMetrics{Metrics: m, Requests: completed}}, nil
}
