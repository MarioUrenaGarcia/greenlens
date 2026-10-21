// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

// Preferences of this browser. Storage may be unavailable, as in a private
// window; the interface then works with the defaults.

export type ThemeChoice = "system" | "dark" | "light";
export type EfficientChoice = "auto" | "on" | "off";

function read(key: string): string | null {
  try {
    return localStorage.getItem(`greenlens.${key}`);
  } catch {
    return null;
  }
}

function write(key: string, value: string): void {
  try {
    localStorage.setItem(`greenlens.${key}`, value);
  } catch {
    // Without storage the choice lasts until the page is closed.
  }
}

export const prefs = {
  lang: () => read("lang"),
  setLang: (v: string) => write("lang", v),
  theme: (): ThemeChoice => {
    const v = read("theme");
    return v === "dark" || v === "light" ? v : "system";
  },
  setTheme: (v: ThemeChoice) => write("theme", v),
  efficient: (): EfficientChoice => {
    const v = read("efficient");
    return v === "on" || v === "off" ? v : "auto";
  },
  setEfficient: (v: EfficientChoice) => write("efficient", v),
  welcomed: () => read("welcome") === "done",
  setWelcomed: () => write("welcome", "done"),
};

const dark = window.matchMedia("(prefers-color-scheme: dark)");

// applyTheme sets the theme on the document, following the system when
// the user has not chosen one.
export function applyTheme(choice: ThemeChoice): void {
  const resolved = choice === "system" ? (dark.matches ? "dark" : "light") : choice;
  document.documentElement.dataset.theme = resolved;
}

export function onSystemTheme(f: () => void): void {
  dark.addEventListener("change", f);
}

export function applyEfficient(on: boolean): void {
  document.documentElement.classList.toggle("efficient", on);
}
