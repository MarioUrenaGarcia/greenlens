// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

// Energy reports of a period, as greenlens report writes them: printable
// from the browser, and as CSV or JSON for other tools.

import { useEffect, useState } from "preact/hooks";
import { api } from "../api";
import { DataTable } from "../components/table";
import { Empty, ErrorNotice, Loading, Metric, Panel, Segmented } from "../components/ui";
import { csv, save, stamp } from "../download";
import * as fmt from "../format";
import { lang, t } from "../i18n";
import { add, factorNote, priced, tariffNote, zero } from "../indicators";
import type { Indicators, Status, UsageEnergy } from "../types";

type Group = "backend" | "model" | "consumer";
type By = "none" | "day" | "hour";

interface Row {
  start: number;
  domains: Record<string, number>;
  componentsJ: number;
  ind: Indicators;
}

interface Report {
  from: string;
  to: string;
  by: By;
  resolution: string;
  rows: Row[];
  total: Row;
  groups: UsageEnergy[];
  groupBy: Group;
  estimated: boolean;
}

function dayStart(d: Date): Date {
  return new Date(d.getFullYear(), d.getMonth(), d.getDate());
}

function inputDate(d: Date): string {
  const p = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}`;
}

export function Reports(props: { status: Status }) {
  const st = props.status;
  const today = dayStart(new Date());
  const [from, setFrom] = useState(inputDate(new Date(today.getTime() - 6 * 864e5)));
  const [to, setTo] = useState(inputDate(today));
  const [by, setBy] = useState<By>("day");
  const [group, setGroup] = useState<Group>("model");
  const [report, setReport] = useState<Report | null>(null);
  const [error, setError] = useState<unknown>(null);
  const levels = new Map(st.domains.map((d) => [d.domain, d.level]));

  useEffect(() => {
    // The end date is included: the report runs to the start of the next day.
    const start = new Date(`${from}T00:00:00`);
    const end = new Date(new Date(`${to}T00:00:00`).getTime() + 864e5);
    if (!(start < end)) return;
    let stale = false;
    setReport(null);
    const bucket = by === "day" ? "24h" : by === "hour" ? "1h" : undefined;
    Promise.all([api.usage(start, end, bucket), api.usageBy(start, end, group)])
      .then(([u, g]) => {
        if (stale) return;
        const rows = new Map<number, Row>();
        const total: Row = { start: start.getTime(), domains: {}, componentsJ: 0, ind: zero() };
        let estimated = false;
        for (const r of u.rows) {
          const k = Date.parse(r.bucket_start);
          const row = rows.get(k) ?? { start: k, domains: {}, componentsJ: 0, ind: zero() };
          row.domains[r.domain] = (row.domains[r.domain] ?? 0) + r.energy_j;
          total.domains[r.domain] = (total.domains[r.domain] ?? 0) + r.energy_j;
          if ((levels.get(r.domain) ?? "component") === "component") {
            row.componentsJ += r.energy_j;
            total.componentsJ += r.energy_j;
            row.ind = add(row.ind, r.indicators);
            total.ind = add(total.ind, r.indicators);
          }
          estimated ||= r.quality === "estimated";
          rows.set(k, row);
        }
        setReport({
          from: u.from,
          to: u.to,
          by,
          resolution: u.resolution,
          rows: [...rows.values()].sort((a, b) => a.start - b.start),
          total,
          groups: g.rows.filter((r) =>
            group === "model" ? r.model : group === "consumer" ? r.consumer : true,
          ),
          groupBy: group,
          estimated,
        });
        setError(null);
      })
      .catch((e: unknown) => !stale && setError(e));
    return () => {
      stale = true;
    };
  }, [from, to, by, group]);

  const f = st.factors;
  const domains = report ? Object.keys(report.total.domains).sort() : [];
  const exportCsv = () => {
    if (!report) return;
    const head = ["start", ...domains.map((d) => `${d}_j`), "components_j", "kwh", "co2e_g", "cost"];
    const lines = report.rows.map((r) => [
      new Date(r.start).toISOString(),
      ...domains.map((d) => r.domains[d] ?? 0),
      r.componentsJ,
      r.ind.kwh,
      r.ind.co2e_g,
      r.ind.cost ?? "",
    ]);
    save(`greenlens-report-${stamp()}.csv`, "text/csv", csv([head, ...lines]));
  };
  const exportJson = () => {
    if (!report) return;
    const doc = {
      schema_version: 1,
      from: report.from,
      to: report.to,
      bucket: report.by,
      resolution: report.resolution,
      estimated: report.estimated,
      emission_factor_g_per_kwh: f.emission_g_per_kwh,
      emission_factor_source: f.emission_source,
      hourly_profile: f.hourly_profile,
      currency: priced(f) ? f.currency : undefined,
      rows: report.rows.map((r) => ({
        start: new Date(r.start).toISOString(),
        domains_j: r.domains,
        components_j: r.componentsJ,
        indicators: r.ind,
      })),
      total: {
        domains_j: report.total.domains,
        components_j: report.total.componentsJ,
        indicators: report.total.ind,
      },
      groups: { group_by: report.groupBy, rows: report.groups },
    };
    save(`greenlens-report-${stamp()}.json`, "application/json", JSON.stringify(doc, null, 2));
  };

  const totals = report ? report.total.ind : null;
  // The agent writes the environmental and SCI reports of the same period
  // itself, as pages to print or files to keep.
  const reportHref = (kind: "esg" | "sci", format: "html" | "csv" | "json") => {
    const q = new URLSearchParams({
      kind,
      format,
      lang: lang(),
      from: new Date(`${from}T00:00:00`).toISOString(),
      to: new Date(new Date(`${to}T00:00:00`).getTime() + 864e5).toISOString(),
    });
    if (kind === "sci") q.set("group_by", "model");
    return `/api/v1/reports?${q}`;
  };
  return (
    <div class="stack">
      <div class="page-head">
        <div>
          <h1>{t("gui.reports.title")}</h1>
          <p>{t("gui.reports.intro")}</p>
        </div>
        <div class="panel-actions no-print">
          <button type="button" class="btn" onClick={() => window.print()} disabled={!report}>
            {t("gui.reports.print")}
          </button>
          <button type="button" class="btn" onClick={exportCsv} disabled={!report}>
            CSV
          </button>
          <button type="button" class="btn" onClick={exportJson} disabled={!report}>
            JSON
          </button>
        </div>
      </div>
      <div class="glass panel no-print">
        <div class="grid">
          <div class="field">
            <label for="report-from">{t("gui.reports.from")}</label>
            <input
              id="report-from"
              class="input"
              type="date"
              value={from}
              max={to}
              onInput={(e) => setFrom((e.target as HTMLInputElement).value)}
            />
          </div>
          <div class="field">
            <label for="report-to">{t("gui.reports.to")}</label>
            <input
              id="report-to"
              class="input"
              type="date"
              value={to}
              min={from}
              onInput={(e) => setTo((e.target as HTMLInputElement).value)}
            />
          </div>
          <div class="field">
            <Segmented
              showLabel
              label={t("gui.reports.by")}
              value={by}
              options={(["none", "day", "hour"] as By[]).map((v) => ({
                value: v,
                label: t(`gui.reports.by_${v}`),
              }))}
              onChange={setBy}
            />
          </div>
          <div class="field">
            <Segmented
              showLabel
              label={t("gui.reports.group")}
              value={group}
              options={(["backend", "model", "consumer"] as Group[]).map((v) => ({
                value: v,
                label: t(`gui.reports.group_${v}`),
              }))}
              onChange={setGroup}
            />
          </div>
        </div>
      </div>
      {error ? <ErrorNotice error={error} /> : null}
      {!report ? (
        error ? null : (
          <Loading />
        )
      ) : (
        <>
          <p>
            {t("gui.reports.period", { from: fmt.dateTime(report.from), to: fmt.dateTime(report.to) })}{" "}
            {report.estimated ? <strong>{t("gui.summary.estimated")}</strong> : null}
          </p>
          <div class="grid">
            <Metric
              label={t("gui.summary.energy")}
              value={fmt.energy(report.total.componentsJ)}
              tone="load"
              note={`${fmt.decimal(totals?.kwh ?? 0, 4)} kWh`}
            />
            <Metric
              label={t("gui.summary.co2e")}
              value={fmt.co2e(totals?.co2e_g ?? 0)}
              tone="load"
              note={factorNote(f)}
            />
            <Metric
              label={t("gui.summary.cost")}
              value={totals?.cost === undefined ? "-" : fmt.money(totals.cost, f.currency)}
              note={tariffNote(f)}
            />
            {totals?.water_l !== undefined ? (
              <Metric label={t("gui.reports.water")} value={`${fmt.decimal(totals.water_l, 2)} L`} />
            ) : null}
          </div>
          <Panel title={t("gui.reports.by_domain")} help="explanation/measurement">
            {report.rows.length === 0 ? (
              <Empty>{t("gui.reports.no_data")}</Empty>
            ) : (
              <DataTable<Row>
                caption={t("gui.reports.by_domain_caption")}
                rows={report.rows}
                rowKey={(r) => String(r.start)}
                columns={[
                  {
                    key: "start",
                    label: t("gui.col.period"),
                    cell: (r) => fmt.dateTime(new Date(r.start).toISOString()),
                  },
                  ...domains.map((d) => ({
                    key: d,
                    label: t(`domain.${d}`),
                    numeric: true,
                    cell: (r: Row) => fmt.energy(r.domains[d] ?? 0),
                  })),
                  {
                    key: "components",
                    label: t("gui.summary.energy"),
                    numeric: true,
                    cell: (r) => fmt.energy(r.componentsJ),
                  },
                  {
                    key: "co2e",
                    label: t("gui.summary.co2e"),
                    numeric: true,
                    cell: (r) => fmt.co2e(r.ind.co2e_g),
                  },
                ]}
              />
            )}
          </Panel>
          <Panel title={t("gui.reports.documents")} help="guides/reports">
            <p>{t("gui.reports.documents_intro")}</p>
            <ul class="links no-print">
              {(["esg", "sci"] as const).map((kind) => (
                <li key={kind}>
                  <a href={reportHref(kind, "html")} target="_blank" rel="noopener">
                    {t(`gui.reports.document_${kind}`)}
                  </a>{" "}
                  <span class="muted">
                    (
                    <a href={reportHref(kind, "csv")} download>
                      CSV
                    </a>
                    ,{" "}
                    <a href={reportHref(kind, "json")} download={`greenlens-${kind}.json`}>
                      JSON
                    </a>
                    )
                  </span>
                </li>
              ))}
            </ul>
          </Panel>
          <Panel title={t(`gui.reports.by_group_${report.groupBy}`)} help="explanation/attribution">
            {report.groups.length === 0 ? (
              <Empty>{t("gui.reports.no_groups")}</Empty>
            ) : (
              <DataTable<UsageEnergy>
                caption={t(`gui.reports.by_group_${report.groupBy}`)}
                rows={report.groups}
                rowKey={(r) => `${r.backend}/${r.model}/${r.consumer}`}
                initial={{ key: "energy", desc: true }}
                columns={[
                  {
                    key: "backend",
                    label: t("gui.col.backend"),
                    sort: (r) => r.backend,
                    cell: (r) => r.backend,
                  },
                  ...(report.groupBy === "model"
                    ? [
                        {
                          key: "model",
                          label: t("gui.col.model"),
                          sort: (r: UsageEnergy) => r.model ?? "",
                          cell: (r: UsageEnergy) => r.model,
                        },
                      ]
                    : []),
                  ...(report.groupBy === "consumer"
                    ? [
                        {
                          key: "consumer",
                          label: t("gui.col.consumer"),
                          sort: (r: UsageEnergy) => r.consumer ?? "",
                          cell: (r: UsageEnergy) => r.consumer,
                        },
                      ]
                    : []),
                  {
                    key: "energy",
                    label: t("gui.col.energy"),
                    numeric: true,
                    sort: (r) => r.energy_j,
                    cell: (r) => fmt.energy(r.energy_j),
                  },
                  {
                    key: "co2e",
                    label: t("gui.summary.co2e"),
                    numeric: true,
                    cell: (r) => fmt.co2e(r.indicators?.co2e_g ?? 0),
                  },
                  {
                    key: "tokens",
                    label: t("gui.col.generated_tokens"),
                    numeric: true,
                    cell: (r) => (r.tokens_counted ? fmt.count(r.generated_tokens) : t("gui.not_counted")),
                  },
                  {
                    key: "jpt",
                    label: t("gui.col.joules_per_token"),
                    numeric: true,
                    cell: (r) => (r.generated_tokens > 0 ? fmt.energy(r.energy_j / r.generated_tokens) : "-"),
                  },
                ]}
              />
            )}
          </Panel>
          <p class="muted">
            {t("gui.reports.footer", {
              resolution: report.resolution,
              generated: fmt.dateTime(new Date().toISOString()),
            })}
          </p>
        </>
      )}
    </div>
  );
}
