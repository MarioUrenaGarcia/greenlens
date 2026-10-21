// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

import { useEffect, useState } from "preact/hooks";
import { api } from "../api";
import { PowerInstrument } from "../components/instrument";
import { DataTable } from "../components/table";
import { Empty, ErrorNotice, Metric, Panel, Segmented } from "../components/ui";
import * as fmt from "../format";
import { t } from "../i18n";
import { useLive } from "../live";
import { href } from "../router";
import type { Status, UsageEnergy } from "../types";

export type Period = "24h" | "7d" | "30d";
const periodMs: Record<Period, number> = { "24h": 864e5, "7d": 7 * 864e5, "30d": 30 * 864e5 };

export function periodOptions(): { value: Period; label: string }[] {
  return (["24h", "7d", "30d"] as Period[]).map((v) => ({ value: v, label: t(`gui.period.${v}`) }));
}

interface Totals {
  energyJ: number;
  models: UsageEnergy[];
}

// componentEnergy adds up the energy of the component domains, which do
// not overlap, leaving out platform and wall meters that measure the same
// energy again.
function componentEnergy(status: Status, rows: { domain: string; energy_j: number }[]): number {
  const level = new Map(status.domains.map((d) => [d.domain, d.level]));
  return rows
    .filter((r) => (level.get(r.domain) ?? "component") === "component")
    .reduce((a, r) => a + r.energy_j, 0);
}

export function Summary(props: { status: Status }) {
  const live = useLive();
  const [period, setPeriod] = useState<Period>("24h");
  const [totals, setTotals] = useState<Totals | null>(null);
  const [error, setError] = useState<unknown>(null);
  const st = props.status;

  useEffect(() => {
    const to = new Date();
    const from = new Date(to.getTime() - periodMs[period]);
    let stale = false;
    Promise.all([api.usage(from, to), api.usageBy(from, to, "model")])
      .then(([u, m]) => {
        if (stale) return;
        const models = m.rows.filter((r) => r.model).sort((a, b) => b.energy_j - a.energy_j);
        setTotals({ energyJ: componentEnergy(st, u.rows), models });
        setError(null);
      })
      .catch((e: unknown) => !stale && setError(e));
    return () => {
      stale = true;
    };
  }, [period, st]);

  const f = st.factors;
  const kwh = (totals?.energyJ ?? 0) / fmt.joulesPerKWh;
  const problems = st.problems?.length ?? 0;
  return (
    <div class="stack">
      <div class="page-head">
        <div>
          <h1>{t("gui.summary.title")}</h1>
          <p>{st.estimated ? t("gui.summary.estimated") : t("gui.summary.measured")}</p>
        </div>
        <Segmented
          label={t("gui.period.label")}
          value={period}
          options={periodOptions()}
          onChange={setPeriod}
        />
      </div>
      {problems > 0 ? (
        <p class="notice">
          {t("gui.summary.problems", { count: problems })}{" "}
          <a href={href("sources")}>{t("gui.summary.see_sources")}</a>
        </p>
      ) : null}
      <PowerInstrument points={live.points} connected={live.connected} />
      {error ? <ErrorNotice error={error} /> : null}
      <div class="grid">
        <Metric
          label={t("gui.summary.energy")}
          value={totals ? fmt.energy(totals.energyJ) : "-"}
          tone="load"
          note={t("gui.summary.energy_note")}
        />
        <Metric
          label={t("gui.summary.co2e")}
          value={totals ? fmt.co2e(kwh * f.emission_g_per_kwh * f.pue) : "-"}
          tone="load"
          note={t("gui.summary.factor", {
            value: fmt.decimal(f.emission_g_per_kwh, 1),
            source: f.emission_source,
          })}
        />
        <Metric
          label={t("gui.summary.cost")}
          value={
            totals && f.tariff_per_kwh !== undefined
              ? fmt.money(kwh * f.tariff_per_kwh * f.pue, f.currency)
              : "-"
          }
          note={
            f.tariff_per_kwh === undefined
              ? t("gui.summary.no_tariff")
              : t("gui.summary.tariff", { value: fmt.money(f.tariff_per_kwh, f.currency) })
          }
        />
      </div>
      <Panel title={t("gui.summary.models")} help="explanation/attribution">
        {!totals ? null : totals.models.length === 0 ? (
          <Empty>{t("gui.summary.no_models")}</Empty>
        ) : (
          <DataTable
            caption={t("gui.summary.models_caption")}
            rows={totals.models.slice(0, 5)}
            rowKey={(r) => `${r.backend}/${r.model}`}
            initial={{ key: "energy", desc: true }}
            columns={[
              { key: "model", label: t("gui.col.model"), sort: (r) => r.model ?? "", cell: (r) => r.model },
              { key: "backend", label: t("gui.col.backend"), sort: (r) => r.backend, cell: (r) => r.backend },
              {
                key: "energy",
                label: t("gui.col.energy"),
                numeric: true,
                sort: (r) => r.energy_j,
                cell: (r) => fmt.energy(r.energy_j),
              },
              {
                key: "jpt",
                label: t("gui.col.joules_per_token"),
                numeric: true,
                sort: (r) => (r.generated_tokens ? r.energy_j / r.generated_tokens : 0),
                cell: (r) =>
                  r.tokens_counted && r.generated_tokens ? fmt.energy(r.energy_j / r.generated_tokens) : "-",
              },
              {
                key: "tokens",
                label: t("gui.col.generated_tokens"),
                numeric: true,
                sort: (r) => r.generated_tokens,
                cell: (r) => (r.tokens_counted ? fmt.count(r.generated_tokens) : t("gui.not_counted")),
              },
            ]}
          />
        )}
      </Panel>
    </div>
  );
}
