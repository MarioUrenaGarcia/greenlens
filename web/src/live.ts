// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

// One connection to the live stream of the agent, shared by every screen.
// It closes while the page is hidden and resumes from the last sample it
// received when the page is shown again.

import { useEffect, useState } from "preact/hooks";
import type { BenchmarkJob, Backends, CalibrationJob, LivePoint } from "./types";

export const liveWindowMs = 15 * 60 * 1000;

interface State {
  points: LivePoint[];
  backends: Backends | null;
  benchmark: BenchmarkJob | null;
  calibration: CalibrationJob | null;
  connected: boolean;
}

let state: State = { points: [], backends: null, benchmark: null, calibration: null, connected: false };
let lastId = 0;
let source: EventSource | null = null;
let throttle = 0;
let pending = 0;
const listeners = new Set<(s: State) => void>();

function emit(): void {
  if (pending) return;
  const send = () => {
    pending = 0;
    for (const f of listeners) f(state);
  };
  if (throttle > 0) {
    pending = window.setTimeout(send, throttle);
  } else {
    send();
  }
}

function connect(): void {
  if (source || document.hidden || listeners.size === 0) return;
  const url = lastId > 0 ? `/api/v1/stream?last_event_id=${lastId}` : "/api/v1/stream";
  const es = new EventSource(url, { withCredentials: true });
  source = es;
  es.onopen = () => {
    state = { ...state, connected: true };
    emit();
  };
  es.onerror = () => {
    state = { ...state, connected: false };
    emit();
  };
  es.addEventListener("sample", (e: MessageEvent<string>) => {
    const id = Number(e.lastEventId);
    if (id > 0 && id <= lastId) return;
    if (id > 0) lastId = id;
    const p = JSON.parse(e.data) as LivePoint;
    const cutoff = Date.parse(p.time) - liveWindowMs;
    const points = state.points.filter((x) => Date.parse(x.time) >= cutoff);
    points.push(p);
    state = { ...state, points, connected: true };
    emit();
  });
  es.addEventListener("backends", (e: MessageEvent<string>) => {
    state = { ...state, backends: JSON.parse(e.data) as Backends };
    emit();
  });
  es.addEventListener("benchmark", (e: MessageEvent<string>) => {
    state = { ...state, benchmark: JSON.parse(e.data) as BenchmarkJob };
    emit();
  });
  es.addEventListener("calibration", (e: MessageEvent<string>) => {
    state = { ...state, calibration: JSON.parse(e.data) as CalibrationJob };
    emit();
  });
}

function disconnect(): void {
  source?.close();
  source = null;
  state = { ...state, connected: false };
}

document.addEventListener("visibilitychange", () => {
  if (document.hidden) {
    disconnect();
  } else {
    connect();
  }
});

// setLiveThrottle delays updates to the screens, as the efficient mode
// does; zero updates with every sample.
export function setLiveThrottle(ms: number): void {
  throttle = ms;
}

export function useLive(): State {
  const [s, set] = useState(state);
  useEffect(() => {
    listeners.add(set);
    connect();
    set(state);
    return () => {
      listeners.delete(set);
      if (listeners.size === 0) disconnect();
    };
  }, []);
  return s;
}
