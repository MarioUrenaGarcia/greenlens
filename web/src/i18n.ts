// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

// Messages come from the same catalogs as the command line, served by the
// agent, so the interface has no texts of its own outside them.

import { Unreachable } from "./api";
import type { Catalog } from "./types";

let messages: Record<string, string> = {};
let current = "en";
let available: string[] = ["en"];

export function lang(): string {
  return current;
}

export function languages(): string[] {
  return available;
}

export type Args = Record<string, string | number>;

// t returns the message of key with its {placeholders} replaced. A missing
// key is returned as is, so the gap is visible.
export function t(key: string, args?: Args): string {
  const msg = messages[key] ?? key;
  if (!args) {
    return msg;
  }
  return msg.replace(/\{(\w+)\}/g, (whole, name: string) => (name in args ? String(args[name]) : whole));
}

export function has(key: string): boolean {
  return key in messages;
}

export async function loadCatalog(wanted: string): Promise<void> {
  let resp: Response;
  try {
    resp = await fetch(`/api/v1/catalogs/${encodeURIComponent(wanted)}`, { credentials: "same-origin" });
  } catch (e) {
    throw new Unreachable(String(e));
  }
  if (!resp.ok) {
    throw new Error(`catalog ${wanted}: ${resp.status}`);
  }
  const cat = (await resp.json()) as Catalog;
  messages = cat.messages;
  current = cat.lang;
  available = cat.languages;
  document.documentElement.lang = cat.lang;
}

// pickLanguage follows the choice of the user, then the languages of the
// browser, then English.
export function pickLanguage(saved: string | null): string {
  const supported = ["en", "es"];
  if (saved && supported.includes(saved)) {
    return saved;
  }
  for (const l of navigator.languages ?? [navigator.language]) {
    const base = l.toLowerCase().split("-")[0] ?? "";
    if (supported.includes(base)) {
      return base;
    }
  }
  return "en";
}
