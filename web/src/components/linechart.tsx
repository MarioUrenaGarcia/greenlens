// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

import type { ComponentChildren } from "preact";
import { useEffect, useRef, useState } from "preact/hooks";
import uPlot from "uplot";
import { t } from "../i18n";
import { icons } from "./icons";
import { DataTable } from "./table";

export interface Point {
  time: number;
  value: number | null;
}

function cssVar(name: string): string {
  return getComputedStyle(document.documentElement).getPropertyValue(name).trim() || "#888888";
}

// LineChart draws one series over time, with a table of the same values
// a button away. tone picks amber for consumption or green for efficiency.
export function LineChart(props: {
  label: string;
  points: Point[];
  tone: "load" | "eff";
  format: (v: number) => string;
  formatTime: (ms: number) => string;
  actions?: ComponentChildren;
}) {
  const box = useRef<HTMLDivElement>(null);
  const [asTable, setAsTable] = useState(false);
  useEffect(() => {
    const el = box.current;
    if (!el || asTable) return;
    const ink = cssVar("--ink-muted");
    const grid = cssVar("--glass-border");
    const color = cssVar(props.tone === "eff" ? "--efficiency" : "--load");
    const u = new uPlot(
      {
        width: el.clientWidth || 600,
        height: 220,
        legend: { show: false },
        cursor: { drag: { x: false, y: false } },
        scales: { x: { time: true } },
        axes: [
          { stroke: ink, grid: { stroke: grid }, ticks: { stroke: grid } },
          {
            stroke: ink,
            grid: { stroke: grid },
            ticks: { stroke: grid },
            values: (_u, vals) => vals.map((v) => props.format(v)),
            size: 80,
          },
        ],
        series: [
          {},
          { label: props.label, stroke: color, width: 2, points: { show: true, size: 5 }, spanGaps: false },
        ],
      },
      [props.points.map((p) => p.time / 1000), props.points.map((p) => p.value)],
      el,
    );
    const ro = new ResizeObserver(() => u.setSize({ width: el.clientWidth, height: 220 }));
    ro.observe(el);
    return () => {
      ro.disconnect();
      u.destroy();
    };
  }, [asTable, props.points, props.tone, props.label, props.format]);
  const last = [...props.points].reverse().find((p) => p.value !== null);
  return (
    <div>
      <div class="panel-actions" style={{ justifyContent: "flex-end", marginBottom: "var(--space-2)" }}>
        {props.actions}
        <button type="button" class="btn" aria-pressed={asTable} onClick={() => setAsTable(!asTable)}>
          {asTable ? icons.chart() : icons.table()}
          {asTable ? t("gui.instrument.show_chart") : t("gui.instrument.show_table")}
        </button>
      </div>
      {asTable ? (
        <DataTable
          caption={props.label}
          rows={props.points}
          rowKey={(p) => String(p.time)}
          columns={[
            { key: "time", label: t("gui.col.time"), cell: (p) => props.formatTime(p.time) },
            {
              key: "value",
              label: props.label,
              numeric: true,
              cell: (p) => (p.value === null ? "-" : props.format(p.value)),
            },
          ]}
        />
      ) : (
        <div class="inner instrument-chart">
          <div
            ref={box}
            role="img"
            aria-label={
              last
                ? t("gui.chart.label", { series: props.label, value: props.format(last.value ?? 0) })
                : props.label
            }
          />
        </div>
      )}
    </div>
  );
}
