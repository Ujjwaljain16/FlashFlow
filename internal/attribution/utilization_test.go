package attribution

import (
	"math"
	"testing"
	"time"

	"flashflow/internal/replay"
)

func TestUtilization_HandComputed(t *testing.T) {
	// lambda=8 req/s, mu=10 req/s => serviceTime=1/mu=100ms => rho=0.8.
	got := Utilization(8, 100*time.Millisecond)
	if math.Abs(got-0.8) > 1e-9 {
		t.Errorf("Utilization(8, 100ms) = %v, want 0.8", got)
	}
}

func TestUtilization_ZeroLambdaIsIdle(t *testing.T) {
	if got := Utilization(0, 100*time.Millisecond); got != 0 {
		t.Errorf("Utilization(0, 100ms) = %v, want 0", got)
	}
}

func TestUtilization_Overloaded(t *testing.T) {
	// lambda=20 req/s against a 100ms service time (capacity 10 req/s)
	// => rho=2.0, correctly reporting an overloaded target rather than
	// clamping to 1.0 -- clamping would hide exactly the "arrival rate
	// exceeds capacity" signal a metamorphic monotonicity check needs.
	got := Utilization(20, 100*time.Millisecond)
	if math.Abs(got-2.0) > 1e-9 {
		t.Errorf("Utilization(20, 100ms) = %v, want 2.0 (unclamped)", got)
	}
}

// TestUtilizationFromWorld_HandBuiltFixture constructs a WorldResult
// directly (not via RunWorld) so the expected rho values can be
// hand-computed independently of the replay engine's own behavior.
func TestUtilizationFromWorld_HandBuiltFixture(t *testing.T) {
	targets := []replay.TargetProfile{
		{Name: "fast", ServiceTime: 10 * time.Millisecond},  // capacity 100 req/s
		{Name: "slow", ServiceTime: 100 * time.Millisecond}, // capacity 10 req/s
	}
	result := replay.WorldResult{
		CompletedByTarget: map[string]int{
			"fast": 500, // over a 10s horizon: lambda=50 req/s, rho=50*0.01=0.5
			"slow": 50,  // lambda=5 req/s, rho=5*0.1=0.5
		},
	}
	got, err := UtilizationFromWorld(result, targets, 10*time.Second)
	if err != nil {
		t.Fatalf("UtilizationFromWorld failed: %v", err)
	}
	if math.Abs(got["fast"]-0.5) > 1e-9 {
		t.Errorf("rho[fast] = %v, want 0.5", got["fast"])
	}
	if math.Abs(got["slow"]-0.5) > 1e-9 {
		t.Errorf("rho[slow] = %v, want 0.5", got["slow"])
	}
}

func TestUtilizationFromWorld_TargetWithNoCompletionsIsZero(t *testing.T) {
	targets := []replay.TargetProfile{{Name: "idle", ServiceTime: 10 * time.Millisecond}}
	result := replay.WorldResult{CompletedByTarget: map[string]int{}}
	got, err := UtilizationFromWorld(result, targets, time.Second)
	if err != nil {
		t.Fatalf("UtilizationFromWorld failed: %v", err)
	}
	if got["idle"] != 0 {
		t.Errorf("rho[idle] = %v, want 0", got["idle"])
	}
}

func TestUtilizationFromWorld_RejectsNonPositiveHorizon(t *testing.T) {
	if _, err := UtilizationFromWorld(replay.WorldResult{}, nil, 0); err == nil {
		t.Fatal("expected an error for a zero horizon")
	}
	if _, err := UtilizationFromWorld(replay.WorldResult{}, nil, -time.Second); err == nil {
		t.Fatal("expected an error for a negative horizon")
	}
}
