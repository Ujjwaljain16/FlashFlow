package tuning

import (
	"math/rand"
	"testing"
	"time"

	"flashflow/internal/proxy"
)

func TestConfigSpace_SampleProducesValidWeights(t *testing.T) {
	cs := DefaultConfigSpace()
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 500; i++ {
		cfg := cs.Sample(rng)
		if ok, reason := cs.Valid(cfg); !ok {
			t.Fatalf("sample %d produced an invalid config: %s (%+v)", i, reason, cfg)
		}
	}
}

func TestConfigSpace_SampleIsDeterministicForASeed(t *testing.T) {
	cs := DefaultConfigSpace()
	a := cs.Sample(rand.New(rand.NewSource(42)))
	b := cs.Sample(rand.New(rand.NewSource(42)))
	if a != b {
		t.Fatalf("same-seed samples diverged: %+v vs %+v", a, b)
	}
}

func TestConfigSpace_Valid_RejectsNegativeWeight(t *testing.T) {
	cs := DefaultConfigSpace()
	cfg := proxy.AdaptiveConfig{
		Weights:          proxy.AdaptiveWeights{Load: -0.1, Latency: 0.4, Cache: 0.4, Cost: 0.3},
		ReferenceLatency: 100 * time.Millisecond, StaleAfter: 1000 * time.Millisecond,
	}
	if ok, _ := cs.Valid(cfg); ok {
		t.Fatal("expected a negative weight to be rejected")
	}
}

func TestConfigSpace_Valid_RejectsWeightsNotSummingToOne(t *testing.T) {
	cs := DefaultConfigSpace()
	cfg := proxy.AdaptiveConfig{
		Weights:          proxy.AdaptiveWeights{Load: 0.4, Latency: 0.4, Cache: 0.4, Cost: 0.4},
		ReferenceLatency: 100 * time.Millisecond, StaleAfter: 1000 * time.Millisecond,
	}
	if ok, _ := cs.Valid(cfg); ok {
		t.Fatal("expected weights summing to 1.6 to be rejected")
	}
}

func TestConfigSpace_Valid_RejectsOutOfBoundsDuration(t *testing.T) {
	cs := DefaultConfigSpace()
	cfg := proxy.DefaultAdaptiveConfig()
	cfg.StaleAfter = cs.StaleAfterMax + 1
	if ok, _ := cs.Valid(cfg); ok {
		t.Fatal("expected an out-of-bounds StaleAfter to be rejected")
	}
}

func TestConfigSpace_DefaultAdaptiveConfigIsInBounds(t *testing.T) {
	cs := DefaultConfigSpace()
	if ok, reason := cs.Valid(proxy.DefaultAdaptiveConfig()); !ok {
		t.Fatalf("expected proxy.DefaultAdaptiveConfig() to fall within DefaultConfigSpace: %s", reason)
	}
}

func TestHash_StableForIdenticalConfig(t *testing.T) {
	cfg := proxy.DefaultAdaptiveConfig()
	if Hash(cfg) != Hash(cfg) {
		t.Fatal("Hash was not stable for the identical config")
	}
}

func TestHash_DiffersForDifferentConfig(t *testing.T) {
	a := proxy.DefaultAdaptiveConfig()
	b := proxy.DefaultAdaptiveConfig()
	b.StaleAfter += 1
	if Hash(a) == Hash(b) {
		t.Fatal("Hash collided for two configs differing only by 1ns of StaleAfter")
	}
}
