// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

// The days of one model judged against the days before them, next to the
// changes of context that may explain a regression.

import { useEffect, useState } from "preact/hooks";
import { api } from "../api";
import { DataTable } from "../components/table";
import { Empty, ErrorNotice, Loading, Panel } from "../components/ui";
import * as fmt from "../format";
import { t } from "../i18n";
import type { ContextEvent, Finding, Regressions } from "../types";

// concerns reports whether a change of context may affect the model: the
// changes of the node, of its backend and of its own file.
function concerns(e: ContextEvent, backend: string, model: string): boolean {
  switch (e.kind) {
    case "backend_version":
      return e.subject === backend;
    case "model_digest":
      return e.subject === `${backend}/${model}`;
  }
  return true;
}

export function ModelRegressions(props: { backend: string; model: string; fromMs: number }) {
  const [doc, setDoc] = useState<Regressions | null>(null);
  const [error, setError] = useState<unknown>(null);
  useEffect(() => {
    const q = new URLSearchParams({
      from: new Date(props.fromMs).toISOString(),
      to: new Date().toISOString(),
      backend: props.backend,
      model: props.model,
    });
    let stale = false;
    setDoc(null);
    api
      .request<Regressions>("GET", `/api/v1/regressions?${q}`)
      .then((d) => !stale && setDoc(d))
      .catch((e: unknown) => !stale && setError(e));
    return () => {
      stale = true;
    };
  }, [props.backend, props.model, props.fromMs]);
  const days = doc?.models.find((m) => m.backend === props.backend && m.model === props.model)?.days ?? [];
  const events = (doc?.events ?? []).filter((e) => concerns(e, props.backend, props.model));
  const flagged = days.filter((d) => d.status === "regression" || d.status === "improvement");
  return (
    <Panel title={t("gui.regress.title")} help="explanation/regressions">
      {error ? (
        <ErrorNotice error={error} />
      ) : !doc ? (
        <Loading />
      ) : (
        <>
          <p>
            {flagged.length === 0
              ? t("gui.regress.none", { z: doc.params.z_threshold, days: doc.params.baseline_days })
              : t("gui.regress.some", { count: flagged.length })}
          </p>
          {days.length === 0 ? (
            <Empty>{t("gui.regress.no_days", { tokens: fmt.count(doc.params.min_generated_tokens) })}</Empty>
          ) : (
            <DataTable<Finding>
              caption={t("gui.regress.caption")}
              rows={days}
              rowKey={(d) => d.day}
              initial={{ key: "day", desc: true }}
              columns={[
                { key: "day", label: t("gui.regress.day"), sort: (d) => d.day, cell: (d) => d.day },
                {
                  key: "jpt",
                  label: t("gui.col.joules_per_token"),
                  numeric: true,
                  sort: (d) => d.j_per_generated_token,
                  cell: (d) => fmt.energy(d.j_per_generated_token),
                },
                {
                  key: "baseline",
                  label: t("gui.regress.baseline"),
                  numeric: true,
                  sort: (d) => d.baseline_mean,
                  cell: (d) =>
                    d.baseline_days > 1
                      ? `${fmt.energy(d.baseline_mean)} ± ${fmt.energy(d.baseline_sd)}`
                      : "-",
                },
                {
                  key: "z",
                  label: "z",
                  numeric: true,
                  sort: (d) => d.z ?? 0,
                  cell: (d) => (d.z === undefined ? "-" : fmt.decimal(d.z, 2)),
                },
                {
                  key: "status",
                  label: t("gui.regress.status"),
                  sort: (d) => d.status,
                  cell: (d) => <span class={`status-${d.status}`}>{t(`gui.regress.state.${d.status}`)}</span>,
                },
              ]}
            />
          )}
          <h3>{t("gui.regress.changes")}</h3>
          {events.length === 0 ? (
            <Empty>{t("gui.regress.no_changes")}</Empty>
          ) : (
            <ul class="events">
              {events.map((e) => (
                <li key={e.id}>
                  <time dateTime={e.at}>{fmt.dateTime(e.at)}</time> {t(`gui.regress.kind.${e.kind}`)}
                  {e.subject ? ` ${e.subject}` : ""}:{" "}
                  {e.previous ? t("gui.regress.change", { from: e.previous, to: e.value }) : e.value}
                </li>
              ))}
            </ul>
          )}
        </>
      )}
    </Panel>
  );
}
