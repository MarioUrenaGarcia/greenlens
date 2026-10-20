// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

import { render } from "preact";

function App() {
  return (
    <main class="app">
      <h1>GreenLens</h1>
    </main>
  );
}

const root = document.getElementById("app");
if (root) {
  render(<App />, root);
}
