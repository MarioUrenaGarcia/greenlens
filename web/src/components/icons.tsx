// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

// Icons drawn for GreenLens, 24 by 24 with a 1.75 stroke. They are
// decorative: the text next to them carries the meaning.

import type { ComponentChildren } from "preact";

function Svg({ children }: { children: ComponentChildren }) {
  return (
    <svg
      class="icon"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      stroke-width="1.75"
      stroke-linecap="round"
      stroke-linejoin="round"
      aria-hidden="true"
      focusable="false"
    >
      {children}
    </svg>
  );
}

export const icons = {
  logo: () => (
    <svg class="icon" viewBox="0 0 24 24" aria-hidden="true" focusable="false">
      <circle cx="11" cy="11" r="7.5" fill="none" stroke="var(--efficiency)" stroke-width="2" />
      <path d="M7.5 13.5c0-4 3-6.5 7-6.5 0 4-2.5 7-6.5 7" fill="var(--efficiency)" opacity="0.85" />
      <path d="M16.5 16.5L21 21" stroke="var(--load)" stroke-width="2.25" stroke-linecap="round" />
    </svg>
  ),
  summary: () => (
    <Svg>
      <path d="M4 15a8 8 0 1 1 16 0" />
      <path d="M12 15l4-5" />
      <path d="M4 19h16" />
    </Svg>
  ),
  models: () => (
    <Svg>
      <path d="M12 3l8 4.5v9L12 21l-8-4.5v-9z" />
      <path d="M12 12l8-4.5M12 12L4 7.5M12 12v9" />
    </Svg>
  ),
  backends: () => (
    <Svg>
      <rect x="4" y="4" width="16" height="6" rx="1.5" />
      <rect x="4" y="14" width="16" height="6" rx="1.5" />
      <path d="M8 7h.01M8 17h.01" />
    </Svg>
  ),
  alerts: () => (
    <Svg>
      <path d="M6 16V11a6 6 0 0 1 12 0v5l1.5 2h-15z" />
      <path d="M10 20.5a2 2 0 0 0 4 0" />
      <path d="M12 3v2" />
    </Svg>
  ),
  consumers: () => (
    <Svg>
      <circle cx="9" cy="8" r="3" />
      <path d="M3 20c0-3.3 2.7-6 6-6s6 2.7 6 6" />
      <path d="M16 5.5a3 3 0 0 1 0 5M18 14.5c1.8.9 3 2.9 3 5.5" />
    </Svg>
  ),
  benchmarks: () => (
    <Svg>
      <circle cx="12" cy="13" r="7" />
      <path d="M12 13V9.5M9.5 2.5h5M18.5 6.5l1.5-1.5" />
    </Svg>
  ),
  reports: () => (
    <Svg>
      <path d="M7 3h7l4 4v14H7z" />
      <path d="M14 3v4h4M10 12h5M10 16h5" />
    </Svg>
  ),
  sources: () => (
    <Svg>
      <path d="M9 3v5M15 3v5M7 8h10v3a5 5 0 0 1-10 0z" />
      <path d="M12 16v5" />
    </Svg>
  ),
  config: () => (
    <Svg>
      <path d="M4 6h10M18 6h2M4 12h4M12 12h8M4 18h12M20 18h0" />
      <circle cx="16" cy="6" r="2" />
      <circle cx="10" cy="12" r="2" />
      <circle cx="18" cy="18" r="2" />
    </Svg>
  ),
  help: () => (
    <Svg>
      <circle cx="12" cy="12" r="9" />
      <path d="M9.5 9.5a2.5 2.5 0 1 1 3.5 2.3c-.6.3-1 .9-1 1.6v.6M12 17h.01" />
    </Svg>
  ),
  about: () => (
    <Svg>
      <circle cx="12" cy="12" r="9" />
      <path d="M12 11v6M12 7.5h.01" />
    </Svg>
  ),
  table: () => (
    <Svg>
      <rect x="4" y="5" width="16" height="14" rx="1.5" />
      <path d="M4 10h16M4 14.5h16M10 5v14" />
    </Svg>
  ),
  chart: () => (
    <Svg>
      <path d="M4 19h16M4 15l5-5 4 3 7-7" />
    </Svg>
  ),
  signOut: () => (
    <Svg>
      <path d="M14 4h5v16h-5M10 8l-4 4 4 4M6 12h10" />
    </Svg>
  ),
  sort: () => (
    <Svg>
      <path d="M8 9l4-4 4 4M8 15l4 4 4-4" />
    </Svg>
  ),
  up: () => (
    <Svg>
      <path d="M8 14l4-4 4 4" />
    </Svg>
  ),
  down: () => (
    <Svg>
      <path d="M8 10l4 4 4-4" />
    </Svg>
  ),
};
