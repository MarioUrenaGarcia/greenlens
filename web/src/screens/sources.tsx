// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

// The visual equivalent of greenlens doctor: which channels the agent
// found, which it uses and why, and how to fix each problem.

import { DataTable } from "../components/table";
import { Empty, Metric, Panel } from "../components/ui";
import * as fmt from "../format";
import { t } from "../i18n";
import { href } from "../router";
import type { Channel, Problem, Status } from "../types";

function args(
  p: { args?: Record<string, string> } | { reason_args?: Record<string, string> },
): Record<string, string> {
  return ("args" in p ? p.args : "reason_args" in p ? p.reason_args : undefined) ?? {};
}

export function ProblemList(props: { problems: Problem[] }) {
  return (
    <ul class="problems">
      {props.problems.map((p, i) => (
        <li key={`${p.code}-${i}`} class="inner notice">
          <p>
            <strong>
              <a href={href("help", "reference", "errors")}>{p.code}</a>:{" "}
              {t(`errors.${p.code}.message`, args(p))}
            </strong>
          </p>
          <p class="muted">{t(`errors.${p.code}.cause`, args(p))}</p>
          <p>{t(`errors.${p.code}.fix`, args(p))}</p>
        </li>
      ))}
    </ul>
  );
}

export function Sources(props: { status: Status }) {
  const st = props.status;
  const cal = st.calibration;
  const yes = t("gui.yes");
  const no = t("gui.no");
  return (
    <div class="stack">
      <div class="page-head">
        <div>
          <h1>{t("gui.sources.title")}</h1>
          <p>{st.estimated ? t("gui.summary.estimated") : t("gui.summary.measured")}</p>
        </div>
      </div>
      <Panel title={t("gui.sources.problems")} help="guides/permissions">
        {st.problems && st.problems.length > 0 ? (
          <ProblemList problems={st.problems} />
        ) : (
          <Empty>{t("gui.sources.no_problems")}</Empty>
        )}
      </Panel>
      <Panel title={t("gui.sources.channels")} help="explanation/measurement">
        <DataTable<Channel>
          caption={t("gui.sources.channels_caption")}
          rows={st.channels}
          rowKey={(c) => c.id}
          initial={{ key: "selected", desc: true }}
          columns={[
            {
              key: "domain",
              label: t("gui.col.domain"),
              sort: (c) => c.domain,
              cell: (c) => t(`domain.${c.domain}`),
            },
            {
              key: "source",
              label: t("gui.col.source"),
              sort: (c) => c.source,
              cell: (c) => <code>{c.source}</code>,
            },
            { key: "id", label: t("gui.col.channel"), sort: (c) => c.id, cell: (c) => <code>{c.id}</code> },
            {
              key: "quality",
              label: t("gui.col.quality"),
              sort: (c) => c.quality,
              cell: (c) => t(`quality.${c.quality}`),
            },
            {
              key: "selected",
              label: t("gui.col.used"),
              sort: (c) => (c.selected ? 1 : 0),
              cell: (c) => (c.selected ? yes : no),
            },
            {
              key: "reason",
              label: t("gui.col.reason"),
              cell: (c) => t(c.reason, args(c)),
            },
          ]}
        />
      </Panel>
      <Panel title={t("gui.sources.calibration")} help="guides/agent">
        {cal ? (
          <div class="grid">
            <Metric label={t("gui.sources.calibrated_at")} value={fmt.dateTime(cal.at)} />
            <Metric label={t("gui.sources.idle_cpu")} value={fmt.power(cal.baseline.cpu_w)} tone="load" />
            {cal.baseline.dram_w > 0 ? (
              <Metric label={t("gui.sources.idle_dram")} value={fmt.power(cal.baseline.dram_w)} tone="load" />
            ) : null}
            {cal.idle_now_w ? (
              <Metric
                label={t("gui.sources.idle_now")}
                value={fmt.power(cal.idle_now_w)}
                note={t("gui.sources.drift", { percent: fmt.decimal((cal.drift ?? 0) * 100, 0) })}
              />
            ) : null}
          </div>
        ) : (
          <p>{t("gui.sources.not_calibrated")}</p>
        )}
      </Panel>
      <Panel title={t("gui.sources.agent")}>
        <div class="grid">
          <Metric label={t("gui.sources.version")} value={st.version} />
          <Metric
            label={t("gui.sources.started")}
            value={fmt.dateTime(st.started_at)}
            note={t("gui.sources.samples", { count: fmt.count(st.samples) })}
          />
          <Metric
            label={t("gui.sources.self_energy")}
            value={fmt.energy(st.self.energy_j)}
            note={t("gui.sources.self_cpu", { seconds: fmt.decimal(st.self.cpu_seconds, 1) })}
          />
          <Metric
            label={t("gui.sources.memory")}
            value={`${fmt.decimal(st.self.rss_bytes / 1048576, 1)} MiB`}
          />
        </div>
      </Panel>
    </div>
  );
}
