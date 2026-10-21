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
