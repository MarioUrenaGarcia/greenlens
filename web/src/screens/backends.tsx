// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

import { useEffect, useState } from "preact/hooks";
import { api } from "../api";
import { DataTable } from "../components/table";
import { Empty, ErrorNotice, Metric, Panel } from "../components/ui";
import * as fmt from "../format";
import { t } from "../i18n";
import { useLive } from "../live";
import { href } from "../router";
import type { Backend, Backends, Model } from "../types";
import { ProblemList } from "./sources";

export function jpt(m: { energy_j: number; generated_tokens: number }): number | null {
  return m.generated_tokens > 0 ? m.energy_j / m.generated_tokens : null;
}

function BackendPanel(props: { b: Backend }) {
  const b = props.b;
  const yes = t("gui.yes");
  const no = t("gui.no");
  return (
    <Panel title={b.name} help="guides/backends">
      <p class="muted">
        <span class="badge">{b.kind}</span>{" "}
        {b.version ? t("gui.backends.version", { version: b.version }) : null}{" "}
        {b.endpoint ? <code>{b.endpoint}</code> : null}
      </p>
      {b.problem ? <ProblemList problems={[b.problem]} /> : null}
      <div class="grid">
        <Metric
          label={t("gui.backends.power")}
          value={fmt.power(b.power_w)}
          tone="load"
          note={t("gui.backends.power_note")}
        />
        <Metric
          label={t("gui.backends.energy")}
          value={fmt.energy(b.energy_j)}
          tone="load"
          note={t("gui.backends.split", {
            cpu: fmt.energy(b.cpu_energy_j),
            dram: fmt.energy(b.dram_energy_j),
            gpu: fmt.energy(b.gpu_energy_j),
          })}
        />
        <Metric
          label={t("gui.backends.processes")}
          value={fmt.count(b.processes)}
          note={(b.roots ?? []).map((r) => `${r.name} (${r.pid})`).join(", ") || undefined}
        />
        <Metric
          label={t("gui.backends.tokens")}
          value={b.token_counters ? yes : no}
          note={b.token_counters ? undefined : t("gui.backends.no_tokens")}
        />
      </div>
      {b.models && b.models.length > 0 ? (
        <DataTable<Model>
          caption={t("gui.backends.models_caption", { backend: b.name })}
          rows={b.models}
          rowKey={(m) => m.name}
          initial={{ key: "energy", desc: true }}
          columns={[
            {
              key: "name",
              label: t("gui.col.model"),
              sort: (m) => m.name,
              cell: (m) => <a href={href("models", b.name, m.name)}>{m.name}</a>,
            },
            {
              key: "energy",
              label: t("gui.col.energy"),
              numeric: true,
              sort: (m) => m.energy_j,
              cell: (m) => fmt.energy(m.energy_j),
            },
            {
              key: "tokens",
              label: t("gui.col.generated_tokens"),
              numeric: true,
              sort: (m) => m.generated_tokens,
              cell: (m) => fmt.count(m.generated_tokens),
            },
            {
              key: "jpt",
              label: t("gui.col.joules_per_token"),
              numeric: true,
              sort: (m) => jpt(m) ?? 0,
              cell: (m) => {
                const v = jpt(m);
                return v === null ? "-" : fmt.energy(v);
              },
            },
            {
              key: "regression",
              label: t("gui.col.regression"),
              cell: (m) =>
                m.regression
                  ? t("gui.backends.regression", {
                      prompt: fmt.energy(m.regression.prompt_j_per_token),
                      generated: fmt.energy(m.regression.generated_j_per_token),
                      r2: fmt.decimal(m.regression.r2, 2),
                    })
                  : "-",
            },
            { key: "method", label: t("gui.col.method"), cell: (m) => <code>{m.method}</code> },
          ]}
        />
      ) : null}
    </Panel>
  );
}

export function BackendsScreen() {
  const live = useLive();
  const [first, setFirst] = useState<Backends | null>(null);
  const [error, setError] = useState<unknown>(null);
  useEffect(() => {
    api.backends().then(setFirst).catch(setError);
  }, []);
  const doc = live.backends ?? first;
  return (
    <div class="stack">
      <div class="page-head">
        <div>
          <h1>{t("gui.backends.title")}</h1>
          <p>{t("gui.backends.intro")}</p>
        </div>
      </div>
      {error ? <ErrorNotice error={error} /> : null}
      {doc && doc.backends.length === 0 ? (
        <div class="glass panel">
          <Empty>{t("gui.backends.none")}</Empty>
        </div>
      ) : null}
      {[...(doc?.backends ?? [])]
        .sort((a, b) => b.energy_j - a.energy_j)
        .map((b) => (
          <BackendPanel key={b.name} b={b} />
        ))}
    </div>
  );
}
