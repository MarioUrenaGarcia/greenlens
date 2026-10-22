// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

// How the screens write an alert: its state, its series and its value.

import * as fmt from "../format";
import { t } from "../i18n";
import type { Alert } from "../types";

// alertValue writes the value of a signal in its unit.
export function alertValue(expr: string, v: number): string {
  switch (expr) {
    case "node_power_watts":
    case "backend_power_watts":
      return fmt.power(v);
    case "model_joules_per_token":
      return fmt.energy(v);
    case "source_up":
      return v >= 1 ? t("gui.alerts.up") : t("gui.alerts.down");
    default:
      return fmt.decimal(v, 2);
  }
}

export function series(labels: Record<string, string>): string {
  const parts = Object.keys(labels)
    .sort()
    .map((k) => `${k}=${labels[k]}`);
  return parts.length > 0 ? parts.join(", ") : t("gui.alerts.node");
}

export function AlertState(props: { state: Alert["state"] }) {
  return <span class={`alert-${props.state}`}>{t(`gui.alerts.state.${props.state}`)}</span>;
}
