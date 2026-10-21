// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

import { useEffect, useState } from "preact/hooks";
import { api } from "../api";
import { DataTable } from "../components/table";
import { Empty, ErrorNotice, Loading, Panel, Segmented } from "../components/ui";
import * as fmt from "../format";
import { t } from "../i18n";
import type { UsageEnergy } from "../types";
import { jpt } from "./backends";
import { type Period, periodOptions } from "./summary";

const periodMs: Record<Period, number> = { "24h": 864e5, "7d": 7 * 864e5, "30d": 30 * 864e5 };

// Consumers shows the energy each team or application used, as the proxy
// tells them apart, for showback.
export function Consumers() {
  const [period, setPeriod] = useState<Period>("30d");
  const [rows, setRows] = useState<UsageEnergy[] | null>(null);
  const [error, setError] = useState<unknown>(null);
  useEffect(() => {
    const to = new Date();
    const from = new Date(to.getTime() - periodMs[period]);
    let stale = false;
    setRows(null);
    api
      .usageBy(from, to, "consumer")
      .then((u) => !stale && setRows(u.rows.filter((r) => r.consumer)))
      .catch((e: unknown) => !stale && setError(e));
    return () => {
      stale = true;
    };
  }, [period]);
  return (
    <div class="stack">
      <div class="page-head">
        <div>
          <h1>{t("gui.consumers.title")}</h1>
          <p>{t("gui.consumers.intro")}</p>
        </div>
        <Segmented
          label={t("gui.period.label")}
          value={period}
          options={periodOptions()}
          onChange={setPeriod}
        />
      </div>
      {error ? <ErrorNotice error={error} /> : null}
      <Panel title={t("gui.consumers.list")} help="guides/proxy">
        {!rows ? (
          error ? null : (
            <Loading />
          )
        ) : rows.length === 0 ? (
          <Empty>{t("gui.consumers.none")}</Empty>
        ) : (
          <DataTable
            caption={t("gui.consumers.caption")}
            rows={rows}
            rowKey={(r) => `${r.consumer}/${r.backend}/${r.model}`}
            initial={{ key: "energy", desc: true }}
            columns={[
              {
                key: "consumer",
                label: t("gui.col.consumer"),
                sort: (r) => r.consumer ?? "",
                cell: (r) => r.consumer,
              },
              { key: "backend", label: t("gui.col.backend"), sort: (r) => r.backend, cell: (r) => r.backend },
              {
                key: "model",
                label: t("gui.col.model"),
                sort: (r) => r.model ?? "",
                cell: (r) => r.model || "-",
              },
              {
                key: "energy",
                label: t("gui.col.energy"),
                numeric: true,
                sort: (r) => r.energy_j,
                cell: (r) => fmt.energy(r.energy_j),
              },
              {
                key: "tokens",
                label: t("gui.col.generated_tokens"),
                numeric: true,
                sort: (r) => r.generated_tokens,
                cell: (r) => fmt.count(r.generated_tokens),
              },
              {
                key: "jpt",
                label: t("gui.col.joules_per_token"),
                numeric: true,
                sort: (r) => jpt(r) ?? 0,
                cell: (r) => {
                  const v = jpt(r);
                  return v === null ? "-" : fmt.energy(v);
                },
              },
              {
                key: "requests",
                label: t("gui.col.requests"),
                numeric: true,
                sort: (r) => r.requests,
                cell: (r) => fmt.count(r.requests),
              },
            ]}
          />
        )}
      </Panel>
    </div>
  );
}
