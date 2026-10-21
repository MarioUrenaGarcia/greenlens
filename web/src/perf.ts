// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

// The processing time of the interface itself: the renders of Preact and
// the work done after each paint, such as redrawing charts. The browser
// also spends time on layout, painting and compositing that a page cannot
// measure, so this is a lower bound.

import { options } from "preact";

let busyMs = 0;
const openedAt = performance.now();
const window10s: { at: number; busy: number }[] = [];

export function measure(f: () => void): void {
  const t0 = performance.now();
  try {
    f();
  } finally {
    busyMs += performance.now() - t0;
  }
}

const schedule = options.debounceRendering;
options.debounceRendering = (cb) => {
  const run = () => measure(cb);
  if (schedule) schedule(run);
  else queueMicrotask(run);
};

const afterPaint = options.requestAnimationFrame;
options.requestAnimationFrame = (cb) => {
  const run = () => measure(cb);
  if (afterPaint) afterPaint(run);
  else requestAnimationFrame(run);
};

window.setInterval(() => {
  window10s.push({ at: performance.now(), busy: busyMs });
  while (window10s.length > 7) window10s.shift();
}, 10000);

export interface SelfUse {
  // busyShare is the fraction of the last minute spent processing.
  busyShare: number;
  busySeconds: number;
  openSeconds: number;
}

export function selfUse(): SelfUse {
  const now = performance.now();
  const first = window10s[0];
  const share = first && now > first.at ? (busyMs - first.busy) / (now - first.at) : 0;
  return { busyShare: share, busySeconds: busyMs / 1000, openSeconds: (now - openedAt) / 1000 };
}
