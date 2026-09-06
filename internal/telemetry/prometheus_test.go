package telemetry

import (
	"strings"
	"testing"
)

func TestWriteText_RendersAllFields(t *testing.T) {
	m := Metrics{
		RequestsTotal:  map[string]uint64{"edge-a": 100, "edge-b": 50},
		LatencySeconds: map[string]float64{"edge-a": 0.025},
		CacheHits:      10, CacheMisses: 5, CacheFills: 5,
		CoalesceLeads: 3, CoalesceShared: 7,
		HealthState: map[string]string{"edge-a": "HEALTHY", "edge-b": "DEGRADED"},
	}
	var sb strings.Builder
	if err := WriteText(&sb, m); err != nil {
		t.Fatalf("WriteText failed: %v", err)
	}
	out := sb.String()

	for _, want := range []string{
		`flashflow_requests_total{target="edge-a"} 100`,
		`flashflow_requests_total{target="edge-b"} 50`,
		`flashflow_latency_seconds{target="edge-a"} 0.025`,
		`flashflow_cache_hits_total 10`,
		`flashflow_cache_misses_total 5`,
		`flashflow_cache_fills_total 5`,
		`flashflow_coalesce_leads_total 3`,
		`flashflow_coalesce_shared_total 7`,
		`flashflow_target_health{target="edge-a",state="HEALTHY"} 1`,
		`flashflow_target_health{target="edge-b",state="DEGRADED"} 1`,
		"# HELP flashflow_requests_total",
		"# TYPE flashflow_requests_total gauge",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing expected line %q\nfull output:\n%s", want, out)
		}
	}
}

func TestWriteText_OmitsEmptyMaps(t *testing.T) {
	var sb strings.Builder
	if err := WriteText(&sb, Metrics{}); err != nil {
		t.Fatalf("WriteText failed: %v", err)
	}
	out := sb.String()
	if strings.Contains(out, "flashflow_requests_total") {
		t.Error("expected no flashflow_requests_total section for an empty RequestsTotal map")
	}
	if strings.Contains(out, "flashflow_target_health") {
		t.Error("expected no flashflow_target_health section for an empty HealthState map")
	}
	// Scalar counters are always emitted, even at zero -- a scraper
	// distinguishes "zero" from "metric doesn't exist yet" and the
	// latter can trigger different alerting behavior.
	if !strings.Contains(out, "flashflow_cache_hits_total 0") {
		t.Error("expected flashflow_cache_hits_total to be emitted at 0")
	}
}

func TestWriteText_IncludesHistogramWhenPresent(t *testing.T) {
	h := NewHistogram()
	for i := 0; i < 100; i++ {
		h.Record(10_000_000) // 10ms
	}
	var sb strings.Builder
	if err := WriteText(&sb, Metrics{Histogram: h}); err != nil {
		t.Fatalf("WriteText failed: %v", err)
	}
	out := sb.String()
	if !strings.Contains(out, "flashflow_latency_histogram_seconds") {
		t.Error("expected a histogram section when Histogram is set and non-empty")
	}
	if !strings.Contains(out, "flashflow_latency_histogram_seconds_count 100") {
		t.Errorf("expected the histogram count line to report 100, got:\n%s", out)
	}
}

func TestWriteText_OmitsHistogramWhenNilOrEmpty(t *testing.T) {
	var sb strings.Builder
	if err := WriteText(&sb, Metrics{Histogram: nil}); err != nil {
		t.Fatalf("WriteText failed: %v", err)
	}
	if strings.Contains(sb.String(), "flashflow_latency_histogram_seconds") {
		t.Error("expected no histogram section when Histogram is nil")
	}

	sb.Reset()
	if err := WriteText(&sb, Metrics{Histogram: NewHistogram()}); err != nil {
		t.Fatalf("WriteText failed: %v", err)
	}
	if strings.Contains(sb.String(), "flashflow_latency_histogram_seconds") {
		t.Error("expected no histogram section when Histogram is empty (Count()==0)")
	}
}

func TestWriteText_DeterministicOrdering(t *testing.T) {
	m := Metrics{RequestsTotal: map[string]uint64{"z": 1, "a": 2, "m": 3}}
	var first, second strings.Builder
	if err := WriteText(&first, m); err != nil {
		t.Fatalf("WriteText failed: %v", err)
	}
	if err := WriteText(&second, m); err != nil {
		t.Fatalf("WriteText failed: %v", err)
	}
	if first.String() != second.String() {
		t.Error("expected two WriteText calls on identical Metrics to produce byte-identical output")
	}
	// "a" should appear before "m" before "z" in the sorted output.
	out := first.String()
	if strings.Index(out, `target="a"`) > strings.Index(out, `target="m"`) ||
		strings.Index(out, `target="m"`) > strings.Index(out, `target="z"`) {
		t.Errorf("expected labels sorted alphabetically, got:\n%s", out)
	}
}
