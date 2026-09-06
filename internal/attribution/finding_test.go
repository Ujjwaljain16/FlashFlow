package attribution

import (
	"strings"
	"testing"
)

func TestSeverityBand_Boundaries(t *testing.T) {
	cases := []struct {
		rho  float64
		want string
	}{
		{0.69, "comfortably within capacity"},
		{0.7, "approaching capacity"},
		{0.89, "approaching capacity"},
		{0.9, "near saturation"},
		{0.99, "near saturation"},
		{1.0, "at or beyond capacity (overloaded)"},
		{1.5, "at or beyond capacity (overloaded)"},
	}
	for _, c := range cases {
		if got := severityBand(c.rho); got != c.want {
			t.Errorf("severityBand(%v) = %q, want %q", c.rho, got, c.want)
		}
	}
}

func TestExplain_ProducesTextMentioningTargetAndRho(t *testing.T) {
	f := Explain("edge-a", 0.85)
	if f.Target != "edge-a" || f.Rho != 0.85 {
		t.Errorf("Explain: got Target=%q Rho=%v, want edge-a/0.85", f.Target, f.Rho)
	}
	if f.ComparedRho != nil {
		t.Error("Explain: expected ComparedRho to be nil for a single-target finding")
	}
	if !strings.Contains(f.Text, "edge-a") || !strings.Contains(f.Text, "0.850") {
		t.Errorf("Explain: text %q should mention the target name and rho", f.Text)
	}
}

func TestCompare_DetectsMeaningfulDifference(t *testing.T) {
	f := Compare("edge-a", 0.9, "edge-b", 0.5)
	if f.ComparedRho == nil || *f.ComparedRho != 0.5 {
		t.Fatalf("Compare: expected ComparedRho=0.5, got %v", f.ComparedRho)
	}
	if f.ComparedName != "edge-b" {
		t.Errorf("Compare: ComparedName = %q, want edge-b", f.ComparedName)
	}
	if !strings.Contains(f.Text, "higher") {
		t.Errorf("Compare: expected text to describe edge-a as higher utilization, got %q", f.Text)
	}
}

func TestCompare_DetectsSimilar(t *testing.T) {
	f := Compare("edge-a", 0.50, "edge-b", 0.52)
	if !strings.Contains(f.Text, "similar") {
		t.Errorf("Compare: expected text to describe these as similar (diff < 0.05), got %q", f.Text)
	}
}

func TestCompare_DetectsLower(t *testing.T) {
	f := Compare("edge-a", 0.3, "edge-b", 0.9)
	if !strings.Contains(f.Text, "lower") {
		t.Errorf("Compare: expected text to describe edge-a as lower utilization, got %q", f.Text)
	}
}
