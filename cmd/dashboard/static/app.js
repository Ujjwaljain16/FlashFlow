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
loadPolicies();
loadExperimentGroups();
loadTuning();
