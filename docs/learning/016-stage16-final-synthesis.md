# Stage 16 Learning Notes — Closing Out a Project That Kept Correcting Itself

### What did the project originally think it was building?

A load-balancer comparison tool with good statistical hygiene. The earliest framing was close to
"benchmark several routing algorithms properly, with real determinism and real significance testing,"
and the Adaptive router was conceived as the eventual winner the whole platform existed to showcase.

### What did it actually become?

A instrument for discovering that its own original question was underspecified. By the end, the project
wasn't comparing policies anymore — it was measuring a mechanism (committed backlog, fraction-of-time-
over-capacity) that explains WHY policies differ, with the six routing algorithms serving as six
different, mechanistically-distinguishable ways of relating to that mechanism rather than six
competitors for a leaderboard slot.

### Which early assumptions were wrong?

That "does Adaptive beat EWMA" was a well-posed question at all. It implicitly assumed a fixed topology,
a fixed capacity model, and that the answer wouldn't depend on target count or workload shape. Every one
of those assumptions turned out to matter enough to change the conclusion. A second wrong assumption,
smaller but real: that a single fixed threshold (the 0.5 diversion-share value used since Stage 14) would
generalize automatically to a different target count. It didn't — Stage 16's own flagship reproduction
caught it undercounting Adaptive's true committed backlog by roughly 10x on a 5-target topology, purely
because nobody had asked whether "half of all traffic" means the same thing at N=3 and N=5.

### Which experiment most changed the architecture?

Stage 12's finite-capacity model. Before it, the virtual engine had no queueing at all — every request's
latency was exactly its target's fixed service time. Adding a single FIFO queue per target, gated by a
`Capacity` field, was a small code change (an additive field on `TargetProfile`, no rewrite of `RunWorld`)
that made an entire category of behavior — backlog formation, committed work, drain time — observable for
the first time. Every subsequent stage's central finding depends on that one addition.

### Which experiment most changed the scientific interpretation?

Stage 14's full six-policy sweep at N=8, the one that found round-robin beating EWMA. Up to that point,
the project had a clean, appealing two-regime story (load-blind vs. load-aware) that explained every
result seen so far. That one experiment — run specifically to attack the story, not to confirm it — broke
it in a single afternoon. Everything from Stage 15 onward exists because that result had to be explained,
not explained away.

### Which negative result is most valuable?

Adaptive's own worst-of-six P99 in the canonical scenario, confirmed across three independent seeds in
this very stage. It would have been easy — and would have made for a better-sounding final story — to
quietly emphasize Adaptive's better MEAN and let the P99 number sit unremarked in a JSON file nobody reads
closely. Reporting it prominently, in the flagship demo itself, is the single most credibility-building
decision made in this stage, precisely because it costs the project's own preferred narrative something
real.

### What would you not build again?

The literal one-off `offeredRhoPerTarget`/`maxQueueDepth`/`topKShare` helper functions duplicated across
nine separate `cmd/experiment-014*` files in Stage 14. Every one of those helpers computed something
`internal/backlog` now computes once, correctly, with a hand-verified test. Stage 14 had already noticed
the duplication was getting uncomfortable by its ninth experiment; Stage 15 finally consolidated it.
Building the shared package one stage earlier — as soon as the third or fourth copy of the same 15-line
function appeared — would have saved real time and avoided at least one of the small bugs (the `cap`
builtin-shadowing issue, caught and fixed three separate times across different files) that
duplication makes easy to reintroduce.

### What remains genuinely interesting?

Whether the two collapse shapes this project found — acute over-commitment and chronic
under-provisioning — are truly exhaustive, or whether a third shape exists that neither committed backlog
nor fraction-of-time-over-capacity would catch. The falsification program this stage's predecessor ran
was thorough but not exhaustive; a topology or workload this project never constructed could easily reveal
a third failure signature the same way round-robin's chronic pattern revealed the second one. The
cache-affinity interim-latency effect, left explicitly unresolved, is the most concrete open lead: it's a
real, reproducible effect with no correct mechanistic explanation yet offered.

### What did the project teach about experimentation itself?

That a result which looks too clean is the one to distrust hardest, not celebrate — this lesson recurred
at every stage, from Stage 13's capacity-scaling bug (rho appearing to rise cleanly with capacity, which
would have supported exactly the wrong conclusion) to Stage 15's own onset-detection bug (EWMA's
committed backlog reading as a suspiciously small 1) to this very stage's threshold-scaling discovery
(committed backlog dropping to single digits the moment genuine seed variation was introduced). None of
these were caught by being more careful in the abstract; they were caught by insisting that a number
match everything else known about the scenario before trusting it, and being willing to go looking for
the discrepancy instead of writing the encouraging number down. A project that changes its own explanation
five times and can show its work at every step is more trustworthy than one that got the story right on
the first attempt — largely because the second kind of project is usually one that stopped looking too
soon.
