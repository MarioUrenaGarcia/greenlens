// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

// The agent computes the indicators of every row hour by hour, with the
// emission factor and the price of each hour; the interface only adds them
// up and says which factors applied.

import * as fmt from "./format";
import { t } from "./i18n";
import type { Factors, Indicators } from "./types";

export function zero(): Indicators {
  return { kwh: 0, co2e_g: 0 };
}

function plus(a: number | undefined, b: number | undefined): number | undefined {
  if (a === undefined) return b;
  if (b === undefined) return a;
  return a + b;
}

// add returns a + b; a cost or a water use stays missing only when both
// lack it.
export function add(a: Indicators, b: Indicators | undefined): Indicators {
  if (!b) return a;
  const out: Indicators = { kwh: a.kwh + b.kwh, co2e_g: a.co2e_g + b.co2e_g };
  const cost = plus(a.cost, b.cost);
  if (cost !== undefined) out.cost = cost;
  const water = plus(a.water_l, b.water_l);
  if (water !== undefined) out.water_l = water;
  return out;
}

// priced reports whether the agent has a tariff.
export function priced(f: Factors): boolean {
  return f.tariff_per_kwh !== undefined || f.tariff_periods > 0;
}

export function factorNote(f: Factors): string {
  return f.hourly_profile
    ? t("gui.summary.factor_profile", { zone: f.time_zone })
    : t("gui.summary.factor", { value: fmt.decimal(f.emission_g_per_kwh, 1), source: f.emission_source });
}

export function tariffNote(f: Factors): string {
  if (f.tariff_periods > 0)
    return t("gui.summary.tariff_periods", { count: f.tariff_periods, zone: f.time_zone });
  if (f.tariff_per_kwh !== undefined)
    return t("gui.summary.tariff", { value: fmt.money(f.tariff_per_kwh, f.currency) });
  return t("gui.summary.no_tariff");
}
