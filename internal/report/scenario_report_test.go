package report

import (
	"strings"
	"testing"
)

// These tests exist because of a real bug (found in a from-scratch audit,
// not by this package's own prior tests): RenderExplanation's numbered
// narrative used to print "Traffic concentrated on X" and "The policy
// diverted new traffic away" UNCONDITIONALLY whenever CongestionFound was
// true, regardless of what Classify itself had already decided about
// concentration -- directly contradicting the classification printed one
// line below it (reproduced live for round-robin: CHRONIC_COLLAPSE,
// "never genuinely concentrated," yet the narrative said "concentrated").
// Every prior test in this package checked Classify's raw numbers, never
// the rendered text a human actually reads -- exactly how this shipped
// undetected. These tests check the rendered string, not just the
// classifier's return value.

func explanationFor(m Metrics, class Classification, reason string) string {
	sr := ScenarioReport{Policies: []PolicyReport{
		{Policy: "test-policy", Metrics: m, Classification: class, Reason: reason, Mechanism: "TEST MECHANISM"},
	}}
	return sr.RenderExplanation("test-policy")
}

func TestRenderExplanation_UnconcentratedChronicDoesNotClaimConcentration(t *testing.T) {
	// round-robin's own shape: congested, barely above fair share, never
	// drains -- Classify calls this ChronicCollapse specifically BECAUSE
	// it never concentrated. The narrative must agree with that, not
	// contradict it.
	m := Metrics{
		Bottleneck: "edge-04", CongestionFound: true, ConcentrationRatio: 1.1,
		FirstCongestionMs: 2326, DiversionFound: true, CommittedWork: 4, FirstDiversionMs: 2395,
		PeakDepth: 52, FractionAboveCapacity: 0.71, Drained: false,
	}
	out := explanationFor(m, ChronicCollapse, "received only 1.1x its fair share (never genuinely concentrated) and never drained")

	if strings.Contains(out, "Traffic concentrated on") {
		t.Errorf("RenderExplanation claims concentration for a policy Classify says never concentrated:\n%s", out)
	}
	if strings.Contains(out, "reflects a real reactive correction") {
		t.Errorf("RenderExplanation calls a non-reactive share fluctuation a \"real reactive correction\":\n%s", out)
	}
	if !strings.Contains(out, "never meaningfully concentrated") {
		t.Errorf("RenderExplanation should explicitly say traffic was never meaningfully concentrated:\n%s", out)
	}
}

func TestRenderExplanation_ConcentratedAcuteClaimsConcentrationAndReaction(t *testing.T) {
	// EWMA's own shape: genuinely concentrates (well above the 1.2x
	// fair-share gate) before eventually diverting -- the narrative
	// SHOULD describe this as concentration and a reactive correction.
	m := Metrics{
		Bottleneck: "edge-02", CongestionFound: true, ConcentrationRatio: 4.4,
		FirstCongestionMs: 2488, DiversionFound: true, CommittedWork: 97, FirstDiversionMs: 2698,
		PeakDepth: 95, FractionAboveCapacity: 0.55, Drained: true, DrainAtMs: 6851,
	}
	out := explanationFor(m, AcuteCollapse, "concentrated 4.4x its fair share and committed 97 requests (97x capacity) before the episode resolved")

	if !strings.Contains(out, "Traffic concentrated on edge-02") {
		t.Errorf("RenderExplanation should claim concentration for a genuinely concentrated policy:\n%s", out)
	}
	if !strings.Contains(out, "reflects a real reactive correction") {
		t.Errorf("RenderExplanation should describe a concentrated policy's diversion as a reactive correction:\n%s", out)
	}
}

func TestRenderExplanation_AlwaysIncludesClassifierReason(t *testing.T) {
	// The classifier's own Reason string is the single authoritative
	// explanation of WHY a classification landed where it did (e.g. why
	// a concentrated-and-diverted run still ended up Stable, not Acute --
	// because committed work never crossed the severity threshold). The
	// numbered narrative must surface it verbatim, not silently omit the
	// deciding factor.
	m := Metrics{
		Bottleneck: "edge-00", CongestionFound: true, ConcentrationRatio: 1.7,
		FirstCongestionMs: 2355, DiversionFound: true, CommittedWork: 8, FirstDiversionMs: 2421,
		PeakDepth: 38, FractionAboveCapacity: 0.25, Drained: true, DrainAtMs: 4365, Capacity: 1,
	}
	reason := "committed only 8 requests (8.0x capacity) before the episode resolved, regardless of concentration (1.7x fair share)"
	out := explanationFor(m, Stable, reason)

	if !strings.Contains(out, reason) {
		t.Errorf("RenderExplanation output missing the classifier's own reason %q:\n%s", reason, out)
	}
}

func TestRenderExplanation_NeverCongested(t *testing.T) {
	m := Metrics{Bottleneck: "edge-00", CongestionFound: false}
	out := explanationFor(m, Stable, "the target never exceeded its own capacity")
	if strings.Contains(out, "Traffic concentrated on") {
		t.Errorf("a target that never congested should never be described as concentrated:\n%s", out)
	}
	if !strings.Contains(out, "never exceeded its own capacity") {
		t.Errorf("expected the never-congested short-circuit message:\n%s", out)
	}
}
