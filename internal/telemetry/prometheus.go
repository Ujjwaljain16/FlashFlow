package telemetry

import (
	"fmt"
	"io"
	"sort"
)

// WriteText renders m in the Prometheus text-exposition format (the
// plain `# HELP` / `# TYPE` / `metric{labels} value` line format any
// Prometheus-compatible scraper understands) -- hand-rolled per Stage
// 10's confirmed design decision rather than adding
// prometheus/client_golang, matching Histogram's own hand-rolled
// rationale. Labels are sorted by target/key name so repeated calls
// against unchanged Metrics produce byte-identical output, useful for
// tests and for anyone diffing scrapes.
func WriteText(w io.Writer, m Metrics) error {
	writeGauge := func(name, help string, values map[string]uint64) error {
		if len(values) == 0 {
			return nil
		}
		if _, err := fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s gauge\n", name, help, name); err != nil {
			return err
		}
		for _, target := range sortedKeys(values) {
			if _, err := fmt.Fprintf(w, "%s{target=%q} %d\n", name, target, values[target]); err != nil {
				return err
			}
		}
		return nil
	}
	writeGaugeF := func(name, help string, values map[string]float64) error {
		if len(values) == 0 {
			return nil
		}
		if _, err := fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s gauge\n", name, help, name); err != nil {
			return err
		}
		for _, target := range sortedKeysF(values) {
			if _, err := fmt.Fprintf(w, "%s{target=%q} %g\n", name, target, values[target]); err != nil {
				return err
			}
		}
		return nil
	}
	writeScalar := func(name, help string, value uint64) error {
		_, err := fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s counter\n%s %d\n", name, help, name, name, value)
		return err
	}
	writeLabeledStrings := func(name, help string, values map[string]string) error {
		if len(values) == 0 {
			return nil
		}
		if _, err := fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s gauge\n", name, help, name); err != nil {
			return err
		}
		for _, target := range sortedKeysS(values) {
			// A health STATE is a label value, not a number -- exported
			// as a 1-valued gauge per state, the standard Prometheus
			// idiom for representing an enum ("info metric" pattern),
			// so a scraper can alert on e.g. flashflow_target_health{state="UNHEALTHY"} == 1.
			if _, err := fmt.Fprintf(w, "%s{target=%q,state=%q} 1\n", name, target, values[target]); err != nil {
				return err
			}
		}
		return nil
	}

	if err := writeGauge("flashflow_requests_total", "Total application requests observed per target.", m.RequestsTotal); err != nil {
		return err
	}
	if err := writeGaugeF("flashflow_latency_seconds", "Current smoothed (EWMA) latency estimate per target, in seconds.", m.LatencySeconds); err != nil {
		return err
	}
	if err := writeScalar("flashflow_cache_hits_total", "Total cache hits (fresh or stale).", m.CacheHits); err != nil {
		return err
	}
	if err := writeScalar("flashflow_cache_misses_total", "Total cache misses.", m.CacheMisses); err != nil {
		return err
	}
	if err := writeScalar("flashflow_cache_fills_total", "Total cache entries stored.", m.CacheFills); err != nil {
		return err
	}
	if err := writeScalar("flashflow_coalesce_leads_total", "Total coalesced fetches that actually executed (leaders).", m.CoalesceLeads); err != nil {
		return err
	}
	if err := writeScalar("flashflow_coalesce_shared_total", "Total coalesced fetches that shared another caller's result.", m.CoalesceShared); err != nil {
		return err
	}
	if err := writeLabeledStrings("flashflow_target_health", "Current health state per target (1 = this is the current state).", m.HealthState); err != nil {
		return err
	}

	if m.Histogram != nil && m.Histogram.Count() > 0 {
		if _, err := fmt.Fprintf(w,
			"# HELP flashflow_latency_histogram_seconds Approximate request latency distribution (hand-rolled, logarithmic-bucket histogram -- see internal/telemetry.Histogram).\n"+
				"# TYPE flashflow_latency_histogram_seconds summary\n"); err != nil {
			return err
		}
		// Percentile and its Prometheus "quantile" label fraction are
		// listed as an explicit pair, not computed via p/100.0 at
		// render time -- 99.9/100.0 is not exactly representable in
		// binary floating point, which previously rendered the label as
		// "0.9990000000000001" instead of "0.999".
		quantiles := []struct {
			percentile float64
			label      string
		}{
			{50, "0.5"}, {90, "0.9"}, {95, "0.95"}, {99, "0.99"}, {99.9, "0.999"},
		}
		for _, q := range quantiles {
			ns := m.Histogram.ValueAtPercentile(q.percentile)
			if _, err := fmt.Fprintf(w, "flashflow_latency_histogram_seconds{quantile=\"%s\"} %g\n", q.label, float64(ns)/1e9); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintf(w, "flashflow_latency_histogram_seconds_count %d\n", m.Histogram.Count()); err != nil {
			return err
		}
	}

	return nil
}

func sortedKeys(m map[string]uint64) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedKeysF(m map[string]float64) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedKeysS(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
