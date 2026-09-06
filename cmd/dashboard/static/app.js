// FlashFlow Dashboard frontend. Plain JS, no build step, no framework
// -- every view is a thin rendering of JSON the Go backend already
// produces from the real internal/replay engine or from experiment
// artifacts on disk. See internal/dashboard for what each endpoint
// actually does.

const $ = (sel) => document.querySelector(sel);
const $$ = (sel) => Array.from(document.querySelectorAll(sel));

async function getJSON(url) {
  const res = await fetch(url);
  const data = await res.json();
  if (!res.ok) throw new Error(data.error || res.statusText);
  return data;
}

// ---------- Tabs ----------
$$('.tab-btn').forEach((btn) => {
  btn.addEventListener('click', () => {
    $$('.tab-btn').forEach((b) => b.classList.remove('active'));
    $$('.tab-panel').forEach((p) => p.classList.remove('active'));
    btn.classList.add('active');
    $('#tab-' + btn.dataset.tab).classList.add('active');
  });
});

// ---------- Control Room ----------
// Everything here renders internal/report.ScenarioReport /
// PolicyReport / Metrics JSON directly -- see internal/dashboard/
// canonical.go for what each endpoint computes. Nothing is
// recalculated client-side except simple client-side lookups against
// data the backend already classified (e.g. picking one policy's own
// Metrics fields to build the numbered Explain steps).

let controlReport = null; // last-fetched ScenarioReport, shared by Compare/Mechanism/Explain

function classColorVar(classification) {
  switch (classification) {
    case 'ACUTE_COLLAPSE': return 'var(--bad)';
    case 'CHRONIC_COLLAPSE': return 'var(--accent2)';
    case 'RECOVERY_LIMITED': return 'var(--warn)';
    default: return 'var(--good)';
  }
}

function classBadge(classification) {
  const cls = 'badge-' + classification.toLowerCase();
  return `<span class="badge ${cls}">${classification.replace(/_/g, ' ')}</span>`;
}

function fmtSeconds(ms) {
  return (ms / 1000).toFixed(2) + 's';
}

function policyLabel(name) {
  return name.split('-').map((w) => w[0].toUpperCase() + w.slice(1)).join(' ');
}

// worstFirst mirrors cmd/flashflow's own pickDefaultPolicy ordering --
// the most concerning classification first, so a fresh page load lands
// on the policy most worth explaining, not an arbitrary one.
const WORST_FIRST = ['CHRONIC_COLLAPSE', 'ACUTE_COLLAPSE', 'RECOVERY_LIMITED', 'STABLE'];
function pickDefaultPolicy(policies) {
  for (const want of WORST_FIRST) {
    const hit = policies.find((p) => p.classification === want);
    if (hit) return hit.policy;
  }
  return policies.length ? policies[0].policy : '';
}

function renderPolicyCards(container, policies) {
  container.innerHTML = policies.map((p) => `
    <div class="policy-card">
      <div class="policy-name">${policyLabel(p.policy)}</div>
      <div class="metric-big" style="color:${classColorVar(p.classification)}">${fmtSeconds(p.metrics.p99_ms)}</div>
      <div class="metric-sub">p99 &middot; mean ${fmtSeconds(p.metrics.mean_ms)}</div>
      ${classBadge(p.classification)}
    </div>
  `).join('');
}

function renderBehaviorBars(container, policies) {
  const maxConcentration = Math.max(...policies.map((p) => p.metrics.concentration_ratio), 1);
  const maxBacklog = Math.max(...policies.map((p) => p.metrics.committed_work), 1);
  const maxFraction = 1; // fraction_above_capacity is already 0-1

  const group = (title, valueFn, maxVal, fmt) => `
    <div class="behavior-group">
      <div class="behavior-group-title">${title}</div>
      ${policies.map((p) => {
        const v = valueFn(p);
        const pct = Math.min(100, (v / maxVal) * 100);
        return `
          <div class="behavior-row">
            <span>${policyLabel(p.policy)}</span>
            <div class="bar-track"><div class="bar-fill" style="width:${pct}%;background:${classColorVar(p.classification)}"></div></div>
            <span class="bar-value">${fmt(v)}</span>
          </div>`;
      }).join('')}
    </div>`;

  container.innerHTML =
    group('Concentration (x fair share)', (p) => p.metrics.concentration_ratio, maxConcentration, (v) => v.toFixed(1) + 'x') +
    group('Committed Work (requests)', (p) => p.metrics.committed_work, maxBacklog, (v) => v.toFixed(0)) +
    group('Time Above Capacity', (p) => p.metrics.fraction_above_capacity, maxFraction, (v) => (v * 100).toFixed(0) + '%');
}

function renderExplain(policyName) {
  if (!controlReport) return;
  const pr = controlReport.policies.find((p) => p.policy === policyName);
  const container = $('#explain-content');
  if (!pr) { container.innerHTML = ''; return; }
  const m = pr.metrics;

  let steps = [];
  if (!m.congestion_found) {
    steps.push(`${policyLabel(pr.policy)}'s bottleneck target (${m.bottleneck}) never exceeded its own capacity.`);
  } else {
    steps.push(`Traffic concentrated on <strong>${m.bottleneck}</strong>.`);
    steps.push(`${m.bottleneck} crossed capacity at <span class="step-time">${fmtSeconds(m.first_congestion_ms)}</span>.`);
    if (m.diversion_found) {
      steps.push(`${m.committed_work} additional requests were committed before diversion.`);
      steps.push(`The policy diverted new traffic away at <span class="step-time">${fmtSeconds(m.first_diversion_ms)}</span>.`);
    } else {
      steps.push(`The policy never diverted new traffic away from ${m.bottleneck} at all.`);
    }
    steps.push(`Queue depth reached <strong>${m.peak_depth}</strong> in-flight requests.`);
    steps.push(`The target spent <strong>${(m.fraction_above_capacity * 100).toFixed(0)}%</strong> of the observed horizon over capacity.`);
    steps.push(m.drained
      ? `The queue eventually drained at <span class="step-time">${fmtSeconds(m.drain_at_ms)}</span>.`
      : `The queue never drained within the observed horizon.`);
    steps.push(`Result classified as ${classBadge(pr.classification)}.`);
  }

  const counterfactualRows = controlReport.policies
    .filter((p) => p.policy !== policyName)
    .map((p) => `
      <tr>
        <td>${policyLabel(p.policy)}</td>
        <td class="mono-cell">committed_work=${p.metrics.committed_work}</td>
        <td>${classBadge(p.classification)}</td>
      </tr>`).join('');

  container.innerHTML = `
    <ol class="explain-steps">${steps.map((s) => `<li>${s}</li>`).join('')}</ol>
    <div class="explain-mechanism">
      <div>
        <div class="label">Primary Mechanism</div>
        <div class="value">${pr.mechanism}</div>
      </div>
      <div>
        <div class="label">Confidence</div>
        <div class="value" style="font-size:13px">${pr.confidence}</div>
      </div>
    </div>
    <table class="counterfactual-table">${counterfactualRows}</table>
  `;
}

async function loadControlRoom() {
  $('#control-status').textContent = 'running 6 policies x 3 seeds...';
  try {
    controlReport = await getJSON('/api/canonical/report?seeds=3');
    $('#control-scenario-label').textContent = controlReport.scenario_label;
    renderPolicyCards($('#control-cards'), controlReport.policies);
    renderBehaviorBars($('#control-bars'), controlReport.policies);

    const names = controlReport.policies.map((p) => p.policy);
    const opts = names.map((n) => `<option value="${n}">${policyLabel(n)}</option>`).join('');
    $('#explain-policy').innerHTML = opts;
    $('#timeline-policy').innerHTML = opts;
    $('#div-baseline').innerHTML = opts;
    $('#div-counterfactual').innerHTML = opts;

    const defaultPolicy = pickDefaultPolicy(controlReport.policies);
    $('#explain-policy').value = defaultPolicy;
    $('#timeline-policy').value = defaultPolicy;
    $('#div-baseline').value = defaultPolicy;
    $('#div-counterfactual').value = names.find((n) => n !== defaultPolicy) || defaultPolicy;
    renderExplain(defaultPolicy);

    $('#control-status').textContent = '';
  } catch (e) {
    $('#control-status').textContent = 'error: ' + e.message;
  }
}

$('#control-refresh-btn').addEventListener('click', loadControlRoom);
$('#explain-policy').addEventListener('change', (e) => renderExplain(e.target.value));

// ---------- Control Room: Event Timeline ----------
const DEPTH_COLORS = ['#4f8cff', '#ff9d4f', '#3ddc84', '#c792ea', '#ff5c5c'];

function renderEventTimeline(svg, view) {
  const W = 700, rowH = 60, labelW = 90, padTop = 10;
  const targetNames = Object.keys(view.target_depths).sort();
  const rows = 1 + targetNames.length; // traffic + one per target
  const H = padTop + rows * rowH + 20;
  svg.setAttribute('viewBox', `0 0 ${W} ${H}`);

  const allSeries = [view.traffic, ...targetNames.map((t) => view.target_depths[t])];
  const maxTime = Math.max(...view.traffic.map((p) => p.time_ms), 1);
  const xScale = (t) => labelW + (t / maxTime) * (W - labelW - 10);

  let svgContent = '';
  const rowLabels = ['Traffic', ...targetNames];
  allSeries.forEach((series, rowIdx) => {
    const maxVal = Math.max(...series.map((p) => p.value), 1);
    const rowTop = padTop + rowIdx * rowH;
    const rowBottom = rowTop + rowH - 12;
    const yScale = (v) => rowBottom - (v / maxVal) * (rowH - 18);
    const color = rowIdx === 0 ? '#8b8f98' : DEPTH_COLORS[(rowIdx - 1) % DEPTH_COLORS.length];

    const areaPts = series.map((p) => `${xScale(p.time_ms)},${yScale(p.value)}`).join(' ');
    const baseline = `${xScale(series[series.length - 1].time_ms)},${rowBottom} ${xScale(series[0].time_ms)},${rowBottom}`;
    svgContent += `
      <text x="4" y="${rowTop + 12}" fill="#7d8a9a" font-size="11">${rowLabels[rowIdx]}</text>
      <polygon points="${areaPts} ${baseline}" fill="${color}" opacity="0.18"></polygon>
      <polyline points="${areaPts}" fill="none" stroke="${color}" stroke-width="1.5"></polyline>
      <line x1="${labelW}" y1="${rowBottom}" x2="${W - 10}" y2="${rowBottom}" stroke="#262b33" stroke-width="1"></line>
    `;
  });

  // Congestion/diversion/drain markers, drawn across the full height.
  const markers = [
    ['first_congestion_ms', '#ff5c5c', 'congestion'],
    ['first_diversion_ms', '#f5c542', 'diversion'],
    ['drain_at_ms', '#3ddc84', 'drain'],
  ];
  for (const [field, color, label] of markers) {
    const flagField = field === 'drain_at_ms' ? 'drained' : (field === 'first_diversion_ms' ? 'diversion_found' : 'congestion_found');
    if (!view.metrics[flagField]) continue;
    const x = xScale(view.metrics[field]);
    svgContent += `
      <line x1="${x}" y1="${padTop}" x2="${x}" y2="${H - 16}" stroke="${color}" stroke-width="1" stroke-dasharray="3,3"></line>
      <text x="${x + 3}" y="${H - 4}" fill="${color}" font-size="10">${label} ${fmtSeconds(view.metrics[field])}</text>
    `;
  }

  svg.innerHTML = svgContent;
}

$('#timeline-btn').addEventListener('click', async () => {
  const policy = $('#timeline-policy').value;
  $('#timeline-status').textContent = 'running...';
  try {
    const view = await getJSON(`/api/canonical/timeline?policy=${encodeURIComponent(policy)}&buckets=80`);
    renderEventTimeline($('#control-timeline-svg'), view);
    $('#timeline-status').textContent = '';
  } catch (e) {
    $('#timeline-status').textContent = 'error: ' + e.message;
  }
});

// ---------- Control Room: First Divergence ----------
function renderDivergence(svg, summary) {
  const W = 700, H = 150;
  svg.setAttribute('viewBox', `0 0 ${W} ${H}`);
  const rows = [
    { label: 'Baseline', records: summary.baseline_records, y: 45, color: '#4f8cff' },
    { label: 'Counterfactual', records: summary.counterfactual_records, y: 105, color: '#ff9d4f' },
  ];
  const n = Math.max(rows[0].records.length, rows[1].records.length, 1);
  const xStep = (W - 40) / n;
  const divergedAtIdx = summary.diverged ? summary.divergence_index : -1;

  let content = '';
  for (const row of rows) {
    content += `<text x="10" y="${row.y - 20}" fill="#7d8a9a" font-size="11">${row.label}</text>`;
    content += `<line x1="20" y1="${row.y}" x2="${W - 20}" y2="${row.y}" stroke="#262b33" stroke-width="1"></line>`;
    row.records.forEach((rec, i) => {
      const x = 20 + i * xStep;
      const isDiverged = divergedAtIdx >= 0 && i >= divergedAtIdx;
      const color = isDiverged ? row.color : '#3ddc84';
      content += `<circle cx="${x}" cy="${row.y}" r="3" fill="${color}"></circle>`;
    });
  }
  if (divergedAtIdx >= 0 && divergedAtIdx < n) {
    const x = 20 + divergedAtIdx * xStep;
    content += `<line x1="${x}" y1="15" x2="${x}" y2="${H - 10}" stroke="#ff5c5c" stroke-width="1.5" stroke-dasharray="4,3"></line>`;
    content += `<text x="${x + 4}" y="14" fill="#ff5c5c" font-size="11">first divergence</text>`;
  }
  svg.innerHTML = content;
}

$('#div-btn').addEventListener('click', async () => {
  const baseline = $('#div-baseline').value;
  const counterfactual = $('#div-counterfactual').value;
  $('#div-status').textContent = 'running both...';
  try {
    const summary = await getJSON(`/api/canonical/compare?baseline=${encodeURIComponent(baseline)}&counterfactual=${encodeURIComponent(counterfactual)}&seed=17000`);
    const badges = ['SAME SCENARIO', 'SAME SEED', 'SAME TRAFFIC', 'ISOLATED POLICY STATE']
      .map((b) => `<span class="same-world-badge">&check; ${b}</span>`).join('');
    $('#div-badges').innerHTML = badges;
    renderDivergence($('#div-svg'), summary);

    if (summary.diverged) {
      const bTarget = summary.baseline_records[summary.divergence_index]?.target ?? '?';
      const cTarget = summary.counterfactual_records[summary.divergence_index]?.target ?? '?';
      $('#div-caption').innerHTML =
        `Divergence started at decision #${summary.divergence_index} (t=${fmtSeconds(summary.divergence_time_ms)}): ` +
        `<strong>${policyLabel(baseline)}</strong> &rarr; ${bTarget}, <strong>${policyLabel(counterfactual)}</strong> &rarr; ${cTarget}. ` +
        `From this point onward, the two worlds evolve independently.`;
    } else {
      $('#div-caption').textContent = 'No divergence -- both policies made identical decisions for the entire run.';
    }
    $('#div-status').textContent = '';
  } catch (e) {
    $('#div-status').textContent = 'error: ' + e.message;
  }
});

// ---------- Playground: policies ----------
async function loadPolicies() {
  const policies = await getJSON('/api/playground/policies');
  for (const sel of [$('#run-policy'), $('#cf-baseline'), $('#cf-counterfactual')]) {
    sel.innerHTML = policies.map((p) => `<option value="${p}">${p}</option>`).join('');
  }
  $('#cf-counterfactual').value = policies[policies.length - 1]; // default to adaptive vs first
}

// ---------- Playground: Run ----------
const TARGET_COLORS = ['#4fa3ff', '#ff9d4f', '#4fd18b', '#c792ea', '#ff6767'];

function renderTopology(svg, completedByTarget, rejectedCount) {
  const targets = Object.keys(completedByTarget).sort();
  const total = Object.values(completedByTarget).reduce((a, b) => a + b, 0) + rejectedCount;
  svg.innerHTML = '';
  const cx0 = 60, gap = (360 - 120) / Math.max(targets.length - 1, 1);
  targets.forEach((t, i) => {
    const share = total > 0 ? completedByTarget[t] / total : 0;
    const r = 14 + share * 40;
    const cx = targets.length === 1 ? 180 : cx0 + i * gap;
    const cy = 100;
    const color = TARGET_COLORS[i % TARGET_COLORS.length];
    svg.innerHTML += `
      <circle cx="${cx}" cy="${cy}" r="${r}" fill="${color}" opacity="0.85"></circle>
      <text x="${cx}" y="${cy + r + 16}" fill="#d7dde5" font-size="11" text-anchor="middle">${t}</text>
      <text x="${cx}" y="${cy + 4}" fill="#061018" font-size="11" text-anchor="middle" font-weight="600">${completedByTarget[t]}</text>
      <text x="${cx}" y="${cy + r + 30}" fill="#7d8a9a" font-size="10" text-anchor="middle">${(share * 100).toFixed(1)}%</text>
    `;
  });
}

function renderMetricsTable(table, r) {
  const rows = [
    ['Policy', r.policy],
    ['Total requests', r.total_requests],
    ['Rejected', r.rejected_count],
    ['Mean latency', r.mean_latency_ms.toFixed(1) + ' ms'],
    ['p99 latency', r.p99_latency_ms.toFixed(1) + ' ms'],
  ];
  table.innerHTML = rows.map(([k, v]) => `<tr><td>${k}</td><td>${v}</td></tr>`).join('');
}

function renderTimeline(container, trace, filterText) {
  const f = (filterText || '').toLowerCase();
  const rows = trace.filter((ev) => {
    if (!f) return true;
    return (ev.type + ' ' + (ev.entity || '') + ' ' + JSON.stringify(ev.fields || {})).toLowerCase().includes(f);
  }).slice(0, 500); // cap rendered rows -- a full trace can be thousands of events
  container.innerHTML = rows.map((ev) => `
    <div class="timeline-row">
      <span class="timeline-time">${ev.time_ms.toFixed(1)}ms</span>
      <span class="timeline-type">${ev.type}</span>
      <span class="timeline-entity">${ev.entity || ''}</span>
      <span class="timeline-fields">${ev.fields ? JSON.stringify(ev.fields) : ''}</span>
    </div>
  `).join('') || '<div class="timeline-row">(no matching events)</div>';
}

let lastRunResult = null;

$('#run-btn').addEventListener('click', async () => {
  const policy = $('#run-policy').value;
  $('#run-status').textContent = 'running...';
  try {
    const result = await getJSON('/api/playground/run?policy=' + encodeURIComponent(policy));
    lastRunResult = result;
    $('#run-result').hidden = false;
    renderTopology($('#topology-svg'), result.completed_by_target, result.rejected_count);
    renderMetricsTable($('#run-metrics'), result);
    renderTimeline($('#run-timeline'), result.trace, $('#timeline-filter').value);
    $('#run-status').textContent = `done -- ${result.trace.length} trace events`;
  } catch (e) {
    $('#run-status').textContent = 'error: ' + e.message;
  }
});

$('#timeline-filter').addEventListener('input', (e) => {
  if (lastRunResult) renderTimeline($('#run-timeline'), lastRunResult.trace, e.target.value);
});

// ---------- Playground: Compare ----------
$('#cf-btn').addEventListener('click', async () => {
  const baseline = $('#cf-baseline').value;
  const counterfactual = $('#cf-counterfactual').value;
  $('#cf-status').textContent = 'running both...';
  try {
    const result = await getJSON(`/api/playground/compare?baseline=${encodeURIComponent(baseline)}&counterfactual=${encodeURIComponent(counterfactual)}`);
    $('#cf-result').hidden = false;
    const banner = $('#cf-divergence');
    if (result.diverged) {
      banner.className = 'divergence-banner diverged';
      banner.textContent = `First point of divergence: event #${result.divergence_index} at t=${result.divergence_time_ms.toFixed(1)}ms`;
    } else {
      banner.className = 'divergence-banner identical';
      banner.textContent = 'No divergence -- both traces are byte-for-byte identical (same policy, or policies that happened to make identical decisions throughout).';
    }
    $('#cf-baseline-title').textContent = 'Baseline: ' + baseline;
    $('#cf-cf-title').textContent = 'Counterfactual: ' + counterfactual;
    renderMetricsTable($('#cf-baseline-metrics'), result.baseline);
    renderMetricsTable($('#cf-cf-metrics'), result.counterfactual);
    $('#cf-status').textContent = 'done';
  } catch (e) {
    $('#cf-status').textContent = 'error: ' + e.message;
  }
});

// ---------- Experiments ----------
// loadExperimentGroups/selectGroup build list items via DOM APIs
// (textContent) rather than interpolating names read from disk into an
// innerHTML string -- group/file names come from ListGroups/
// ListResultFiles directory listings, which are HTTP-influenced (the
// directory being listed is a URL parameter), so untrusted-input-shaped
// data should never flow into innerHTML even under this tool's local-only
// threat model (defense in depth).
async function loadExperimentGroups() {
  const groups = await getJSON('/api/experiments');
  const list = $('#exp-groups');
  list.innerHTML = '';
  for (const g of groups) {
    const li = document.createElement('li');
    li.dataset.group = g.name;
    li.appendChild(document.createTextNode(g.name + ' '));
    const span = document.createElement('span');
    span.className = 'muted';
    span.textContent = `(${g.result_file_count})`;
    li.appendChild(span);
    li.addEventListener('click', () => selectGroup(li.dataset.group, li));
    list.appendChild(li);
  }
}

async function selectGroup(name, li) {
  $$('#exp-groups li').forEach((l) => l.classList.remove('selected'));
  if (li) li.classList.add('selected');
  const files = await getJSON('/api/experiments/' + encodeURIComponent(name));
  const list = $('#exp-files');
  list.innerHTML = '';
  for (const f of files) {
    const fli = document.createElement('li');
    fli.dataset.file = f;
    fli.textContent = f;
    fli.addEventListener('click', () => selectFile(name, fli.dataset.file, fli));
    list.appendChild(fli);
  }
  $('#exp-content').textContent = '';
}

async function selectFile(group, file, li) {
  $$('#exp-files li').forEach((l) => l.classList.remove('selected'));
  if (li) li.classList.add('selected');
  const content = await getJSON(`/api/experiments/${encodeURIComponent(group)}/${encodeURIComponent(file)}`);
  $('#exp-content').textContent = JSON.stringify(content, null, 2);
}

// ---------- Tuning ----------
function renderTuningChart(svg, points, bestSoFar) {
  svg.innerHTML = '';
  const valid = points.filter((p) => p.valid);
  if (valid.length === 0) return;
  const utilities = valid.map((p) => p.utility).concat(bestSoFar);
  const min = Math.min(...utilities), max = Math.max(...utilities);
  const range = max - min || 1;
  const W = 600, H = 220, padding = 20;
  const xScale = (i) => padding + (i / Math.max(valid.length - 1, 1)) * (W - 2 * padding);
  const yScale = (u) => H - padding - ((u - min) / range) * (H - 2 * padding);

  const scatterPts = valid.map((p, i) => `${xScale(i)},${yScale(p.utility)}`);
  const bestPts = bestSoFar.map((u, i) => `${xScale(i)},${yScale(u)}`);

  svg.innerHTML = `
    <polyline points="${bestPts.join(' ')}" fill="none" stroke="#4fd18b" stroke-width="2.5"></polyline>
    ${scatterPts.map((pt) => {
      const [x, y] = pt.split(',');
      return `<circle cx="${x}" cy="${y}" r="2" fill="#4fa3ff" opacity="0.5"></circle>`;
    }).join('')}
    <text x="${padding}" y="14" fill="#7d8a9a" font-size="11">best-so-far utility (green) vs each evaluation (blue dots)</text>
  `;
}

function renderHoldoutBars(container, table, h) {
  const maxVal = Math.max(h.baseline_dev_utility, h.winner_dev_utility, h.baseline_holdout_utility, h.winner_holdout_utility, 0.01);
  const bar = (label, val, cls) => `
    <div class="holdout-bar-group">
      <div class="holdout-bar ${cls}" style="height:${(val / maxVal * 100).toFixed(1)}%"></div>
      <div class="holdout-bar-label">${label}<br>${val.toFixed(4)}</div>
    </div>`;
  container.innerHTML =
    bar('Baseline / Dev', h.baseline_dev_utility, 'dev') +
    bar('Winner / Dev', h.winner_dev_utility, 'dev') +
    bar('Baseline / Holdout', h.baseline_holdout_utility, 'holdout') +
    bar('Winner / Holdout', h.winner_holdout_utility, 'holdout');
  table.innerHTML = `
    <tr><td>Generalization gap</td><td>${h.generalization_gap >= 0 ? '+' : ''}${h.generalization_gap.toFixed(4)}</td></tr>
    <tr><td>Evidence tier</td><td>${h.evidence_tier}</td></tr>
  `;
}

async function loadTuning() {
  const summary = await getJSON('/api/tuning');
  if (!summary.available) {
    $('#tuning-unavailable').hidden = false;
    $('#tuning-content').hidden = true;
    return;
  }
  $('#tuning-unavailable').hidden = true;
  $('#tuning-content').hidden = false;
  $('#tuning-meta').textContent = `${summary.tuner_version} -- ${summary.evaluations.length} evaluations -- plateaued: ${summary.plateaued}`;
  renderTuningChart($('#tuning-svg'), summary.evaluations, summary.best_so_far_utility);
  if (summary.holdout) {
    renderHoldoutBars($('#holdout-bars'), $('#holdout-table'), summary.holdout);
  }
}

// ---------- Init ----------
loadControlRoom();
loadPolicies();
loadExperimentGroups();
loadTuning();
