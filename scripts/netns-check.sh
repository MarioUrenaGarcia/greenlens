#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
# Copyright (C) 2026 Mario Ureña García
#
# Runs the agent inside a network namespace that has nothing but loopback,
# uses it as a person would (the command line over the socket, the HTTP API,
# a browser session, the live stream, the interface and the metrics) and
# checks with strace that neither the agent nor the commands ever try to
# reach an address beyond loopback.
#
# Usage: scripts/netns-check.sh [path to greenlens]
# Needs unshare with unprivileged user namespaces, strace and curl. Without
# them the check is skipped, unless GREENLENS_NETNS_REQUIRED is set.
set -euo pipefail
export LC_ALL=C.UTF-8

bin=$(realpath "${1:-bin/greenlens}")
missing=""
for tool in unshare strace curl ip pgrep; do
  command -v "$tool" >/dev/null || missing="$missing $tool"
done
if [ -z "$missing" ] && ! unshare -rn true 2>/dev/null; then
  missing=" unprivileged network namespaces"
fi
if [ -n "$missing" ]; then
  if [ -n "${GREENLENS_NETNS_REQUIRED:-}" ]; then
    echo "netns check: missing$missing" >&2
    exit 1
  fi
  echo "netns check skipped: missing$missing"
  exit 0
fi

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
cat >"$work/greenlens.yaml" <<EOF
agent:
  data_dir: $work/data
  socket: $work/gl.sock
  sample_interval: 200ms
server:
  listen: 127.0.0.1:10060
EOF

# Everything below runs inside the namespace, as its root user, which the
# kernel maps back to the user who started the check.
unshare -rn bash -euo pipefail -c '
  work=$1 bin=$2
  cfg=(--config "$work/greenlens.yaml")
  traced() { name=$1; shift; strace -f -qq -e trace=bind,connect,sendto,sendmsg -o "$work/trace.$name" "$@"; }
  ip link set lo up
  strace -f -qq -e trace=bind,connect,sendto,sendmsg -o "$work/trace.agent" "$bin" agent "${cfg[@]}" >"$work/agent.log" 2>&1 &
  tracer=$!
  for _ in $(seq 100); do
    "$bin" status "${cfg[@]}" >/dev/null 2>&1 && break
    sleep 0.1
  done
  sleep 1
  traced status "$bin" status "${cfg[@]}" -o json >"$work/status.json"
  traced report "$bin" report "${cfg[@]}" --from 10m -o json >"$work/report.json"
  traced doctor "$bin" doctor "${cfg[@]}" >"$work/doctor.txt"
  link=$(traced gui "$bin" gui "${cfg[@]}" --print)
  base=http://127.0.0.1:10060
  curl -fsS -o /dev/null -c "$work/jar" "$link"
  curl -fsS -b "$work/jar" "$base/api/v1/status" >"$work/http-status.json"
  curl -fsS -b "$work/jar" "$base/api/v1/backends" >/dev/null
  curl -fsS -b "$work/jar" "$base/api/v1/help?q=energy" >/dev/null
  curl -fsS "$base/" | grep -q "<div id=\"app\">"
  curl -fsS "$base/sw.js" | grep -q greenlens-
  curl -fsS "$base/metrics" | grep -q "^greenlens_node_energy_joules_total"
  # The stream never ends by itself; two seconds of it are enough.
  curl -sS -N -b "$work/jar" --max-time 2 "$base/api/v1/stream" >"$work/stream.txt" 2>/dev/null || true
  grep -q "^event: sample" "$work/stream.txt"
  # The agent is the child of strace; it must stop cleanly on TERM.
  kill -TERM "$(pgrep -P "$tracer")"
  wait "$tracer"
' netns "$work" "$bin"

grep -q '"samples":' "$work/status.json" || { echo "netns check: status lacks samples" >&2; exit 1; }
if grep -q '"level":"ERROR"' "$work/agent.log"; then
  echo "netns check: the agent logged errors:" >&2
  grep '"level":"ERROR"' "$work/agent.log" >&2
  exit 1
fi
# The trace must show the agent binding its listener, or strace did not
# follow it and an empty trace would prove nothing.
if ! grep -qE 'bind\(.*htons\(10060\).*inet_addr\("127\.0\.0\.1"\)' "$work/trace.agent"; then
  echo "netns check: the trace of the agent lacks its listener" >&2
  exit 1
fi
# Unix sockets, netlink and loopback are local; any other address of the
# internet families is an attempt to leave the machine.
calls=$(cat "$work"/trace.* | grep -cE 'connect\(|sendto\(|sendmsg\(' || true)
outbound=$(cat "$work"/trace.* | grep -E 'connect\(|sendto\(|sendmsg\(' | grep -E 'sa_family=AF_INET6?\b' |
  grep -vE 'inet_addr\("127\.|inet_pton\(AF_INET6, "::1"|"::ffff:127\.' || true)
if [ -n "$outbound" ]; then
  echo "netns check: connections beyond loopback:" >&2
  echo "$outbound" >&2
  exit 1
fi
echo "netns check: agent, command line, HTTP API, stream, interface and metrics work with loopback only; $calls socket calls traced, none beyond loopback"
