package main

import (
	"fmt"
	"net/http"
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
