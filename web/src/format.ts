// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

// Quantities with three significant digits in the unit that suits them, as
// the command line writes them, and numbers in the format of the language.

import { lang } from "./i18n";

function digits(v: number): string {
  const a = Math.abs(v);
  const frac = a >= 100 ? 0 : a >= 10 ? 1 : 2;
  return new Intl.NumberFormat(lang(), { minimumFractionDigits: frac, maximumFractionDigits: frac }).format(
    v,
  );
}

export function energy(j: number): string {
  const a = Math.abs(j);
  if (a === 0) return "0 J";
  if (a < 1) return `${digits(j * 1e3)} mJ`;
  if (a < 3600) return `${digits(j)} J`;
  if (a < 3.6e6) return `${digits(j / 3600)} Wh`;
  return `${digits(j / 3.6e6)} kWh`;
}

export function power(w: number): string {
  const a = Math.abs(w);
  if (a === 0) return "0 W";
  if (a < 1) return `${digits(w * 1e3)} mW`;
  if (a < 1000) return `${digits(w)} W`;
  return `${digits(w / 1000)} kW`;
}

export function co2e(g: number): string {
  const a = Math.abs(g);
  if (a < 1000) return `${digits(g)} gCO2e`;
  if (a < 1e6) return `${digits(g / 1000)} kgCO2e`;
  return `${digits(g / 1e6)} tCO2e`;
}

export function money(v: number, currency: string): string {
  return new Intl.NumberFormat(lang(), { style: "currency", currency, maximumFractionDigits: 4 }).format(v);
}

export function count(n: number): string {
  return new Intl.NumberFormat(lang()).format(n);
}

export function decimal(v: number, fraction = 2): string {
  return new Intl.NumberFormat(lang(), { maximumFractionDigits: fraction }).format(v);
}

export function dateTime(iso: string): string {
  return new Intl.DateTimeFormat(lang(), { dateStyle: "medium", timeStyle: "short" }).format(new Date(iso));
}

export function clock(ms: number): string {
  return new Intl.DateTimeFormat(lang(), { timeStyle: "medium" }).format(new Date(ms));
}

export const joulesPerKWh = 3.6e6;
