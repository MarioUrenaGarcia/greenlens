// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

// perf comes first, so it measures every render.
import "./perf";
import { render } from "preact";
import { App } from "./app";

const root = document.getElementById("app");
if (root) {
  render(<App />, root);
}

// The service worker needs a secure context: localhost, 127.0.0.1 or TLS.
if ("serviceWorker" in navigator && window.isSecureContext) {
  navigator.serviceWorker.register("/sw.js").catch(() => undefined);
}
