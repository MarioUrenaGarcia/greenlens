// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

// The service worker keeps the files of the interface, so it opens as an
// application window and shows that the agent is unreachable instead of a
// browser error. Only those files are kept: answers of the API, the login
// link and the metrics always go to the agent.

// A module, so this declaration narrows the global self of a worker.
export {};

declare const self: ServiceWorkerGlobalScope;
// The build replaces these with the files of the interface and their hash.
declare const __SHELL__: string[];
declare const __VERSION__: string;

const cacheName = `greenlens-${__VERSION__}`;

self.addEventListener("install", (e) => {
  e.waitUntil(
    caches
      .open(cacheName)
      .then((c) => c.addAll(__SHELL__))
      .then(() => self.skipWaiting()),
  );
});

self.addEventListener("activate", (e) => {
  e.waitUntil(
    caches
      .keys()
      .then((keys) =>
        Promise.all(
          keys.filter((k) => k.startsWith("greenlens-") && k !== cacheName).map((k) => caches.delete(k)),
        ),
      )
      .then(() => self.clients.claim()),
  );
});

const shell = new Set(__SHELL__);

self.addEventListener("fetch", (e) => {
  const url = new URL(e.request.url);
  if (e.request.method !== "GET" || url.origin !== self.location.origin || !shell.has(url.pathname)) return;
  // The network comes first, so a new version shows at once; the copy
  // serves when the agent does not answer.
  e.respondWith(
    fetch(e.request)
      .then((resp) => {
        if (resp.ok) {
          const copy = resp.clone();
          void caches.open(cacheName).then((c) => c.put(url.pathname, copy));
        }
        return resp;
      })
      .catch(() => caches.match(url.pathname, { ignoreVary: true }).then((r) => r ?? Response.error())),
  );
});
