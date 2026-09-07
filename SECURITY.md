# Security

FlashFlow is a research laboratory for controlled experiments on distributed edge-routing behavior — see
[Non-Goals](README.md#non-goals). It is not production software, does not run against real user traffic,
and does not handle credentials, payments, or personal data of any kind. Treat findings here accordingly:
a report against this repository is welcome, but the severity ceiling is different than for a production
system.

A security/operations audit of this codebase was already performed and is public:
[`docs/audit/SECURITY_AND_OPERATIONS.md`](docs/audit/SECURITY_AND_OPERATIONS.md). It covers the areas most
relevant to a Go networking project — shell-injection surface, secret/credential handling, and dependency
scope — and its methodology and findings are disclosed there rather than summarized here.

## Reporting a concern

Open a GitHub issue, or use GitHub's private vulnerability reporting (Security tab → "Report a
vulnerability") for anything you'd rather not disclose publicly first. There is no bug bounty or SLA —
this is a solo research project — but reports will be read and acknowledged.
