#!/usr/bin/env bash
# Stage 8's minimal NGINX reference benchmark: FlashFlow's real proxy
# path (cmd/proxy) versus NGINX, both fronting the byte-for-byte
# identical backend (cmd/http-origin, 20ms artificial delay), at light,
# modest load -- 200 requests, concurrency 10, well below either
# system's likely capacity for a single backend.
#
# This is deliberately narrow. Per the master context's own framing:
# this is a reference point, not a claim that FlashFlow replaces
# NGINX. FlashFlow's research capabilities -- deterministic virtual
# execution, counterfactual replay, adaptive routing, statistical
# analysis, automatic tuning -- are outside the scope of this
# proxy-performance comparison entirely.
#
# Requires Docker (for the NGINX container). If Docker isn't available,
# this script exits early with a clear message rather than failing
# opaquely -- Stage 8's other validation does not depend on this script.

set -uo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."

OUT_DIR="experiments/008-tuning-validation/results"
mkdir -p "$OUT_DIR"

if ! docker info >/dev/null 2>&1; then
  echo "Docker is not available or not running -- skipping the NGINX reference benchmark."
  echo "This is a reference-only comparison; it does not gate Stage 8's other validation."
  exit 0
fi

ORIGIN_PID=""
PROXY_PID=""
NGINX_CONTAINER="flashflow-nginx-bench"

cleanup() {
  echo ""
  echo "Cleaning up..."
  [ -n "$ORIGIN_PID" ] && kill "$ORIGIN_PID" 2>/dev/null
  [ -n "$PROXY_PID" ] && kill "$PROXY_PID" 2>/dev/null
  docker rm -f "$NGINX_CONTAINER" >/dev/null 2>&1
}
trap cleanup EXIT

echo "=========================================================================================="
echo " Stage 8: Minimal NGINX Reference Benchmark"
echo "=========================================================================================="

echo ""
echo "Starting cmd/http-origin (:8000, 20ms artificial delay)..."
go run ./cmd/http-origin -addr :8000 -delay-ms 20 &
ORIGIN_PID=$!
sleep 1

echo "Starting cmd/proxy (:8081 -> http-origin, single target)..."
# -debug-headers=false: cmd/proxy defaults this to true, which adds
# X-Request-ID/X-Selected-Edge/X-Upstream-Latency response headers (and
# the small per-request cost of computing them) that NGINX's plain
# passthrough never adds. An adversarial audit of this benchmark's
# first version caught that the two systems' response bytes were not
# actually identical as claimed -- disabled here so the comparison is
# overhead-only, not "FlashFlow plus extra headers" vs. plain NGINX.
go run ./cmd/proxy -addr :8081 -targets http://127.0.0.1:8000 -check-interval-ms 200 -check-timeout-ms 200 -debug-headers=false &
PROXY_PID=$!

# MSYS_NO_PATHCONV=1 disables Git Bash's automatic Unix-to-Windows path
# conversion for this one command. Without it, Git Bash silently
# rewrites the CONTAINER-side path in "-v host:/etc/nginx/nginx.conf"
# into a Windows path too (e.g. "C:/Program Files/Git/etc/nginx/..."),
# producing a nonsense mount -- NGINX then serves its own built-in
# default welcome page instead of the intended config, a genuine HTTP
# 200 with completely wrong content. This was found, not assumed: it
# produced a real "successful" first run reporting NGINX at ~2ms mean
# latency, LOWER than Origin's own 20ms artificial delay could possibly
# allow if actually proxied -- caught only by noticing that
# impossibility and checking the response body directly (it was
# nginx's own welcome page, not Origin's JSON), not by any status-code
# or error-count check, since the broken response was a perfectly
# valid 200.
echo "Starting NGINX container (:8080 -> host.docker.internal:8000)..."
MSYS_NO_PATHCONV=1 docker run -d --rm --name "$NGINX_CONTAINER" \
  -p 8080:80 \
  -v "$(pwd)/deployments/nginx-bench/nginx.conf:/etc/nginx/nginx.conf:ro" \
  nginx:alpine >/dev/null

echo "Waiting for all three to become ready..."
for i in $(seq 1 30); do
  origin_ok=0; proxy_ok=0; nginx_ok=0
  curl -sf http://127.0.0.1:8000/health >/dev/null 2>&1 && origin_ok=1
  curl -sf http://127.0.0.1:8081/ >/dev/null 2>&1 && proxy_ok=1
  curl -sf http://127.0.0.1:8080/ >/dev/null 2>&1 && nginx_ok=1
  if [ "$origin_ok" -eq 1 ] && [ "$proxy_ok" -eq 1 ] && [ "$nginx_ok" -eq 1 ]; then
    break
  fi
  sleep 1
done
if [ "$origin_ok" -ne 1 ] || [ "$proxy_ok" -ne 1 ] || [ "$nginx_ok" -ne 1 ]; then
  echo "One or more services never became ready (origin=$origin_ok proxy=$proxy_ok nginx=$nginx_ok) -- aborting."
  exit 1
fi
echo "All services ready. Health-check settling (2s)..."
sleep 2

# Content verification, not just a 2xx status: both proxies must
# actually be forwarding to Origin, not silently serving something
# else (a default page, a cached response, an error page that happens
# to return 200) that would make the comparison meaningless. This is
# the exact check a first run of this script lacked -- see the
# MSYS_NO_PATHCONV comment above for what it would have caught.
echo "Verifying both endpoints actually reach Origin (not a default page or cached response)..."
PROXY_BODY="$(curl -sf http://127.0.0.1:8081/)"
NGINX_BODY="$(curl -sf http://127.0.0.1:8080/)"
if [[ "$PROXY_BODY" != *'"service":"origin"'* ]]; then
  echo "FlashFlow proxy is not returning Origin's real response body -- aborting rather than benchmarking a broken setup."
  echo "Got: $PROXY_BODY"
  exit 1
fi
if [[ "$NGINX_BODY" != *'"service":"origin"'* ]]; then
  echo "NGINX is not returning Origin's real response body (misconfigured proxy_pass?) -- aborting rather than benchmarking a broken setup."
  echo "Got: $NGINX_BODY"
  exit 1
fi
echo "Both endpoints confirmed reaching Origin."

echo ""
echo "--- Benchmarking FlashFlow proxy (:8081) ---"
go run ./cmd/experiment-008h -url http://127.0.0.1:8081/ -label flashflow-proxy -requests 200 -concurrency 10 -warmup 20 -out "$OUT_DIR/008H-flashflow-proxy.json"

echo ""
echo "--- Benchmarking NGINX (:8080) ---"
go run ./cmd/experiment-008h -url http://127.0.0.1:8080/ -label nginx -requests 200 -concurrency 10 -warmup 20 -out "$OUT_DIR/008H-nginx.json"

echo ""
echo "=========================================================================================="
echo " Comparison (reference point only -- see the caveat below)"
echo "=========================================================================================="
go run ./cmd/experiment-008h \
  -compare-a "$OUT_DIR/008H-flashflow-proxy.json" \
  -compare-b "$OUT_DIR/008H-nginx.json" \
  -compare-out "$OUT_DIR/008H-nginx-reference-benchmark.json"

echo ""
echo "Done."
