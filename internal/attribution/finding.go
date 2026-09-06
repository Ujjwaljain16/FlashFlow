package attribution

import "fmt"

// Finding is one generated, human-readable explanation of a target's
// utilization -- the PRD's "generated causal-explanation text on
// tail-latency spike detection" concretized as a fixed severity-band
// template rather than free-form natural-language generation (this
// project has no LLM-in-the-loop component; see Stage 8's own "AI
// Capabilities: None implemented" finding for why that boundary is
// deliberate).
type Finding struct {
	Target       string
	Rho          float64
	ComparedRho  *float64 // nil for a single-target Explain; set for Compare
	ComparedName string
	Text         string
}

// severityBand returns a fixed, human-readable description of a
// utilization value. Boundaries (0.7, 0.9, 1.0) are deliberately round,
// documented thresholds -- not fit to any dataset -- matching this
// project's preference for explicit, hand-verifiable constants over
// values that would need their own justification.
func severityBand(rho float64) string {
	switch {
	case rho < 0.7:
		return "comfortably within capacity"
	case rho < 0.9:
		return "approaching capacity"
	case rho < 1.0:
		return "near saturation"
	default:
		return "at or beyond capacity (overloaded)"
	}
}

// Explain generates a single-target Finding.
func Explain(target string, rho float64) Finding {
	return Finding{
		Target: target,
		Rho:    rho,
		Text:   fmt.Sprintf("%s is running at utilization rho=%.3f -- %s.", target, rho, severityBand(rho)),
	}
}

// Compare generates a two-target Finding contrasting nameA's utilization
// against nameB's. The comparison band ("meaningfully higher" /
// "meaningfully lower" / "similar") uses a fixed 0.05 absolute-rho
// threshold -- deliberately coarser than floating-point equality, since
// two targets whose measured rho differs by, say, 0.01 are not a
// meaningfully different operating point for this template's purpose.
func Compare(nameA string, rhoA float64, nameB string, rhoB float64) Finding {
	const meaningfulDelta = 0.05
	diff := rhoA - rhoB
	var relation string
	switch {
	case diff > meaningfulDelta:
		relation = fmt.Sprintf("meaningfully higher utilization (rho=%.3f, %s) than %s (rho=%.3f, %s)",
			rhoA, severityBand(rhoA), nameB, rhoB, severityBand(rhoB))
	case diff < -meaningfulDelta:
		relation = fmt.Sprintf("meaningfully lower utilization (rho=%.3f, %s) than %s (rho=%.3f, %s)",
			rhoA, severityBand(rhoA), nameB, rhoB, severityBand(rhoB))
	default:
		relation = fmt.Sprintf("similar utilization to %s (rho=%.3f vs %.3f, both %s)",
			nameB, rhoA, rhoB, severityBand(rhoA))
	}
	rhoBCopy := rhoB
	return Finding{
		Target: nameA, Rho: rhoA, ComparedRho: &rhoBCopy, ComparedName: nameB,
		Text: fmt.Sprintf("%s has %s.", nameA, relation),
	}
}
