package tuning

import "flashflow/internal/proxy"

// TrialResult is one prior Suggest→evaluate round, fed back to a Tuner
// so it can condition its next suggestion on everything tried so far.
// Deliberately narrower than Evaluation (search.go's own ledger entry):
// a Tuner needs only the config, its resulting utility, and whether it
// was valid -- not the full Metrics/Scores/runtime bookkeeping the
// search ledger separately preserves for provenance.
type TrialResult struct {
	Config  proxy.AdaptiveConfig
	Utility float64
	Valid   bool
}

// Tuner is Stage 10's (§10.9) generalization of what RunRandomSearch's
// loop used to do inline: given everything tried so far, suggest the
// next candidate. Random Search, Latin Hypercube Sampling, and
// Bayesian Optimization are all Tuners differing only in HOW they use
// `previous` -- Random Search ignores it entirely, LHS uses only its
// own pre-built design (ignoring `previous` too, since a Latin
// Hypercube's whole design is fixed once its size is known), and
// Bayesian Optimization is the one that actually needs it, to fit a
// surrogate model and maximize an acquisition function against it.
//
// Space and Seed exist on the interface (not just as constructor
// arguments each concrete Tuner happens to have) so RunSearch can
// still perform Stage 9's own defense-in-depth ConfigSpace.Valid()
// check (F-24) and record real provenance (F-18-style "never leave a
// finding's fix accidentally undone by a later refactor") without
// needing a type switch over every concrete Tuner implementation.
type Tuner interface {
	// Suggest returns the next candidate to evaluate, given every prior
	// trial in this run (in submission order). previous is nil on the
	// very first call.
	Suggest(previous []TrialResult) proxy.AdaptiveConfig
	// Space returns the ConfigSpace this Tuner samples within --
	// RunSearch uses this for its own validity gate, independent of
	// whatever internal bounds-checking (if any) the Tuner does itself.
	Space() ConfigSpace
	// Seed returns the root seed this Tuner's own randomness was
	// constructed from, for search-ledger provenance.
	Seed() int64
	// Name identifies which algorithm produced a SearchResult -- the
	// same provenance role TunerVersion played before this refactor,
	// now sourced from the Tuner itself rather than a package constant,
	// since three different algorithms can no longer share one constant
	// name.
	Name() string
}
