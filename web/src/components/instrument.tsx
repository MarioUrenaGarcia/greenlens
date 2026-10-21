// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

// The power instrument: the live power of the node stacked by domain, with
// the current value at the end of the trace and the energy of the period.

import { useEffect, useMemo, useRef, useState } from "preact/hooks";
import uPlot from "uplot";
import * as fmt from "../format";
import { t } from "../i18n";
import type { LivePoint } from "../types";
import { icons } from "./icons";
import { DataTable } from "./table";

// The stacked layers: the components of the node, and the rest that a
// platform or wall meter sees beyond them.
const layers = ["package", "dram", "gpu", "rest"] as const;
type Layer = (typeof layers)[number];
const colorVars: Record<Layer, string> = {
  package: "--load",
  dram: "--load-2",
  gpu: "--load-3",
  rest: "--load-4",
};
const systemDomains = ["wall", "platform", "psys"];

export interface Split {
  time: number;
  w: Record<Layer, number>;
  total: number;
}

export function split(p: LivePoint): Split {
  const w: Record<Layer, number> = { package: 0, dram: 0, gpu: 0, rest: 0 };
  for (const [d, v] of Object.entries(p.power_w)) {
    if (d === "package" || d === "dram" || d === "gpu") w[d] += v;
  }
  const components = w.package + w.dram + w.gpu;
  const system = systemDomains.map((d) => p.power_w[d]).find((v) => v !== undefined);
  if (system !== undefined) w.rest = Math.max(0, system - components);
  return { time: Date.parse(p.time), w, total: components + w.rest };
}

function cssVar(name: string): string {
  return getComputedStyle(document.documentElement).getPropertyValue(name).trim() || "#888888";
}

function chartData(rows: Split[]): uPlot.AlignedData {
  const xs = rows.map((r) => r.time / 1000);
  // Cumulative sums, top layer first: each layer is filled down to zero
  // and the next one is drawn over it.
  const sums = layers.map((_, i) => rows.map((r) => layers.slice(0, i + 1).reduce((a, l) => a + r.w[l], 0)));
  return [xs, ...sums.reverse()];
}

export function PowerInstrument(props: { points: LivePoint[]; connected: boolean }) {
  const box = useRef<HTMLDivElement>(null);
  const plot = useRef<uPlot | null>(null);
  const [asTable, setAsTable] = useState(false);
  const rows = useMemo(() => props.points.map(split), [props.points]);
  const present = layers.filter((l) => rows.some((r) => r.w[l] > 0));
  const last = rows[rows.length - 1];
  const energyJ = props.points.reduce((a, p) => a + (split(p).total * p.interval_ms) / 1000, 0);

  useEffect(() => {
    const el = box.current;
    if (!el || asTable) return;
    const ink = cssVar("--ink-muted");
    const grid = cssVar("--glass-border");
    const opts: uPlot.Options = {
      width: el.clientWidth || 600,
      height: 260,
      legend: { show: false },
      cursor: { drag: { x: false, y: false } },
      scales: { x: { time: true }, y: { range: (_u, _min, max) => [0, Math.max(1, max * 1.1)] } },
      axes: [
        { stroke: ink, grid: { stroke: grid }, ticks: { stroke: grid } },
        {
          stroke: ink,
          grid: { stroke: grid },
          ticks: { stroke: grid },
          values: (_u, vals) => vals.map((v) => fmt.power(v)),
          size: 70,
        },
      ],
      series: [
        {},
        ...[...layers].reverse().map((l) => {
          const c = cssVar(colorVars[l]);
          return { label: t(`gui.layer.${l}`), stroke: c, fill: c, width: 1, points: { show: false } };
        }),
      ],
    };
    const u = new uPlot(opts, chartData([]), el);
    plot.current = u;
    const ro = new ResizeObserver(() => u.setSize({ width: el.clientWidth, height: 260 }));
    ro.observe(el);
    return () => {
      ro.disconnect();
      u.destroy();
      plot.current = null;
    };
  }, [asTable]);

  useEffect(() => {
    plot.current?.setData(chartData(rows));
  }, [rows, asTable]);

  // The amber glow of the background follows the power of the node.
  useEffect(() => {
    if (!last) return;
    const peak = Math.max(...rows.map((r) => r.total), 1);
    document.documentElement.style.setProperty("--load-level", String(0.15 + 0.85 * (last.total / peak)));
  }, [last, rows]);

  const first = rows[0];
  const minutes = first && last ? Math.round((last.time - first.time) / 60000) : 0;
  const label = last
    ? t("gui.instrument.label", { power: fmt.power(last.total), minutes })
    : t("gui.instrument.waiting");
  return (
    <section class="glass instrument" aria-labelledby="instrument-title">
      <div class="panel-head">
        <h2 id="instrument-title">{t("gui.instrument.title")}</h2>
        <div class="panel-actions">
          {!props.connected ? <span class="badge">{t("gui.live.offline")}</span> : null}
          <button type="button" class="btn" aria-pressed={asTable} onClick={() => setAsTable(!asTable)}>
            {asTable ? icons.chart() : icons.table()}
            {asTable ? t("gui.instrument.show_chart") : t("gui.instrument.show_table")}
          </button>
        </div>
      </div>
      <div class="instrument-body">
        {asTable ? (
          <DataTable
            caption={t("gui.instrument.table_caption")}
            rows={rows.slice(-60).reverse()}
            rowKey={(r) => String(r.time)}
            columns={[
              { key: "time", label: t("gui.col.time"), cell: (r) => fmt.clock(r.time) },
              ...present.map((l) => ({
                key: l,
                label: t(`gui.layer.${l}`),
                numeric: true,
                cell: (r: Split) => fmt.power(r.w[l]),
              })),
              { key: "total", label: t("gui.col.total"), numeric: true, cell: (r) => fmt.power(r.total) },
            ]}
          />
        ) : (
          <div class="inner instrument-chart">
            <div ref={box} role="img" aria-label={label} />
            <ul class="legend">
              {present.map((l) => (
                <li key={l}>
                  <span class="swatch" style={{ background: `var(${colorVars[l]})` }} />
                  {t(`gui.layer.${l}`)}
                </li>
              ))}
            </ul>
          </div>
        )}
        <div class="inner instrument-now">
          <p class="metric-label">{t("gui.instrument.now")}</p>
          <p class="metric-value" aria-live="off">
            {last ? fmt.power(last.total) : "-"}
          </p>
          <p class="metric-label">{t("gui.instrument.energy")}</p>
          <p class="metric-value num" style={{ fontSize: "var(--step-2)", color: "var(--ink)" }}>
            {fmt.energy(energyJ)}
          </p>
        </div>
      </div>
    </section>
  );
}
