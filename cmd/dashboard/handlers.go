package main

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"flashflow/internal/dashboard"
)

// handleListGroups serves GET /api/experiments -- the top-level
// experiment list (master context rule 30's "Experiment list").
func handleListGroups(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("method not allowed"))
		return
	}
	groups, err := dashboard.ListGroups()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, groups)
}

// handleExperimentPath serves GET /api/experiments/{group} (list result
// files) and GET /api/experiments/{group}/{file} (one file's content) --
// the "Experiment detail" view (rule 30). Both parameters are validated
// inside the dashboard package before ever touching the filesystem.
func handleExperimentPath(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("method not allowed"))
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/api/experiments/")
	parts := strings.SplitN(rest, "/", 2)
	switch len(parts) {
	case 1:
		if parts[0] == "" {
			writeError(w, http.StatusBadRequest, fmt.Errorf("missing group name"))
			return
		}
		files, err := dashboard.ListResultFiles(parts[0])
		if err != nil {
			writeError(w, http.StatusNotFound, err)
			return
		}
		writeJSON(w, files)
	case 2:
		content, err := dashboard.ReadResultFile(parts[0], parts[1])
		if err != nil {
			writeError(w, http.StatusNotFound, err)
			return
		}
		writeJSON(w, content)
	default:
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid path"))
	}
}

// handlePolicies serves GET /api/playground/policies.
func handlePolicies(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, dashboard.PolicyNames())
}

// handleRun serves GET /api/playground/run?policy=... -- actually
// executes RunWorld against the canonical PlaygroundScenario, live.
func handleRun(w http.ResponseWriter, r *http.Request) {
	policy := r.URL.Query().Get("policy")
	if policy == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("missing policy parameter"))
		return
	}
	result, err := dashboard.RunPlayground(policy)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, result)
}

// handleCompare serves GET /api/playground/compare?baseline=...&counterfactual=...
// -- the counterfactual dashboard view (rule 31), including the first
// point of divergence.
func handleCompare(w http.ResponseWriter, r *http.Request) {
	baseline := r.URL.Query().Get("baseline")
	counterfactual := r.URL.Query().Get("counterfactual")
	if baseline == "" || counterfactual == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("missing baseline or counterfactual parameter"))
		return
	}
	result, err := dashboard.ComparePlayground(baseline, counterfactual)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, result)
}

// handleTuning serves GET /api/tuning -- the tuning view (rule 32).
func handleTuning(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, dashboard.LoadTuningSummary())
}

// handleCanonicalReport serves GET /api/canonical/report?seeds=N -- the
// Control Room's own Compare/Explain/Mechanism-cards data source: every
// policy's classification against the Stage 15/16 canonical scenario,
// run fresh (default 3 seeds, matching the flagship's own convention).
func handleCanonicalReport(w http.ResponseWriter, r *http.Request) {
	seeds := 3
	if raw := r.URL.Query().Get("seeds"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			seeds = n
		}
	}
	result, err := dashboard.RunCanonicalReport(seeds)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, result)
}

// handleCanonicalCompare serves GET /api/canonical/compare?baseline=&
// counterfactual=&seed= -- the First Divergence view: two policies run
// against the identical canonical scenario/seed, with their first point
// of trace divergence.
func handleCanonicalCompare(w http.ResponseWriter, r *http.Request) {
	baseline := r.URL.Query().Get("baseline")
	counterfactual := r.URL.Query().Get("counterfactual")
	if baseline == "" || counterfactual == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("missing baseline or counterfactual parameter"))
		return
	}
	seed := int64(17000)
	if raw := r.URL.Query().Get("seed"); raw != "" {
		if n, err := strconv.ParseInt(raw, 10, 64); err == nil {
			seed = n
		}
	}
	result, err := dashboard.CompareCanonical(baseline, counterfactual, seed)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, result)
}

// handleCanonicalTimeline serves GET /api/canonical/timeline?policy=&
// seed=&buckets= -- the Event Timeline view: one policy's own traffic
// and per-target queue-depth series, plus its congestion/diversion/
// drain marker timestamps.
func handleCanonicalTimeline(w http.ResponseWriter, r *http.Request) {
	policy := r.URL.Query().Get("policy")
	if policy == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("missing policy parameter"))
		return
	}
	seed := int64(17000)
	if raw := r.URL.Query().Get("seed"); raw != "" {
		if n, err := strconv.ParseInt(raw, 10, 64); err == nil {
			seed = n
		}
	}
	buckets := 60
	if raw := r.URL.Query().Get("buckets"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			buckets = n
		}
	}
	result, err := dashboard.RunCanonicalTimeline(policy, seed, buckets)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, result)
}

// handleCanonicalStressMap serves GET /api/canonical/stressmap?policy=&
// seed= -- the Regime Explorer: one policy classified across a compact
// 3x3 heterogeneity x workload grid (report.RunStressMap).
func handleCanonicalStressMap(w http.ResponseWriter, r *http.Request) {
	policy := r.URL.Query().Get("policy")
	if policy == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("missing policy parameter"))
		return
	}
	seed := int64(17900) // matches cmd/flashflow stress-map's own default
	if raw := r.URL.Query().Get("seed"); raw != "" {
		if n, err := strconv.ParseInt(raw, 10, 64); err == nil {
			seed = n
		}
	}
	result, err := dashboard.RunCanonicalStressMap(policy, seed)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, result)
}
