// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

import { useEffect, useMemo, useState } from "preact/hooks";
import { api } from "../api";
import { LineChart, type Point } from "../components/linechart";
import { DataTable } from "../components/table";
import { Empty, ErrorNotice, Loading, Metric, Panel, Segmented } from "../components/ui";
import * as fmt from "../format";
import { t } from "../i18n";
import { useLive } from "../live";
import { href } from "../router";
import type { SCIReport, UsageEnergy } from "../types";
import { jpt } from "./backends";
import { ModelRegressions } from "./regressions";
import { type Period, periodOptions } from "./summary";

const periodMs: Record<Period, number> = { "24h": 864e5, "7d": 7 * 864e5, "30d": 30 * 864e5 };
const bucketOf: Record<Period, { bucket: string; ms: number }> = {
  "24h": { bucket: "1h", ms: 36e5 },
  "7d": { bucket: "24h", ms: 864e5 },
  "30d": { bucket: "24h", ms: 864e5 },
};

// Wh per thousand generated tokens and generated tokens per joule are the
// same efficiency as J per token, in the units people compare.
function whPer1k(r: UsageEnergy): number | null {
  const v = jpt(r);
  return v === null ? null : (v * 1000) / 3600;
}

function tokensPerJ(r: UsageEnergy): number | null {
  return r.energy_j > 0 && r.generated_tokens > 0 ? r.generated_tokens / r.energy_j : null;
}

function useUsage(period: Period, bucket?: string) {
  const [doc, setDoc] = useState<{ rows: UsageEnergy[]; from: number; to: number } | null>(null);
  const [error, setError] = useState<unknown>(null);
  useEffect(() => {
    const to = new Date();
    const from = new Date(to.getTime() - periodMs[period]);
    let stale = false;
    setDoc(null);
    api
      .usageBy(from, to, "model", bucket)
      .then(
        (u) =>
          !stale &&
          setDoc({ rows: u.rows.filter((r) => r.model), from: Date.parse(u.from), to: Date.parse(u.to) }),
      )
      .catch((e: unknown) => !stale && setError(e));
    return () => {
      stale = true;
    };
  }, [period, bucket]);
  return { doc, rows: doc?.rows ?? null, error };
}

// useSCI reads the carbon intensity of every model in the period.
function useSCI(period: Period) {
  const [rep, setRep] = useState<SCIReport | null>(null);
  useEffect(() => {
    const to = new Date();
    const from = new Date(to.getTime() - periodMs[period]);
    let stale = false;
    setRep(null);
    // The table stays useful without it, so an error only leaves the
    // column empty.
    api
      .sci(from, to, "model")
      .then((r) => !stale && setRep(r))
      .catch(() => !stale && setRep(null));
    return () => {
      stale = true;
    };
  }, [period]);
  return rep;
}

function ModelList(props: { period: Period }) {
  const { rows, error } = useUsage(props.period);
  const sci = useSCI(props.period);
  if (error) return <ErrorNotice error={error} />;
  if (!rows) return <Loading />;
  if (rows.length === 0) return <Empty>{t("gui.summary.no_models")}</Empty>;
  const dash = (v: number | null, f: (x: number) => string) => (v === null ? "-" : f(v));
  const bySCI = new Map((sci?.rows ?? []).map((r) => [`${r.backend}/${r.model}`, r.sci.g_per_unit ?? null]));
  const sciOf = (r: UsageEnergy) => bySCI.get(`${r.backend}/${r.model}`) ?? null;
  const unit = sci?.functional_unit ?? "1k_generated_tokens";
  return (
    <>
      <DataTable
        caption={t("gui.models.caption")}
        rows={rows}
        rowKey={(r) => `${r.backend}/${r.model}`}
        initial={{ key: "energy", desc: true }}
        columns={[
          {
            key: "model",
            label: t("gui.col.model"),
            sort: (r) => r.model ?? "",
            cell: (r) => <a href={href("models", r.backend, r.model ?? "")}>{r.model}</a>,
          },
          { key: "backend", label: t("gui.col.backend"), sort: (r) => r.backend, cell: (r) => r.backend },
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
            cell: (r) => (r.tokens_counted ? fmt.count(r.generated_tokens) : t("gui.not_counted")),
          },
          {
            key: "jpt",
            label: t("gui.col.joules_per_token"),
            numeric: true,
            sort: (r) => jpt(r) ?? 0,
            cell: (r) => dash(jpt(r), fmt.energy),
          },
          {
            key: "wh1k",
            label: t("gui.col.wh_per_1k"),
            numeric: true,
            sort: (r) => whPer1k(r) ?? 0,
            cell: (r) => dash(whPer1k(r), (v) => `${fmt.decimal(v, 3)} Wh`),
          },
          {
            key: "tpj",
            label: t("gui.col.tokens_per_joule"),
            numeric: true,
            sort: (r) => tokensPerJ(r) ?? 0,
            cell: (r) => dash(tokensPerJ(r), (v) => fmt.decimal(v, 2)),
          },
          {
            key: "requests",
            label: t("gui.col.requests"),
            numeric: true,
            sort: (r) => r.requests,
            cell: (r) => fmt.count(r.requests),
          },
          {
            key: "sci",
            label: t("gui.sci.column", { unit: t(`gui.sci.unit.${unit}`) }),
            numeric: true,
            sort: (r) => sciOf(r) ?? 0,
            cell: (r) => dash(sciOf(r), fmt.co2e),
          },
        ]}
      />
      {sci && sci.missing.length > 0 ? <p class="muted">{t("gui.sci.without_embodied")}</p> : null}
    </>
  );
}

function ModelDetail(props: { backend: string; model: string; period: Period }) {
  const b = bucketOf[props.period];
  const { doc, rows, error } = useUsage(props.period, b.bucket);
  const live = useLive();
  const liveModel = live.backends?.backends
    .find((x) => x.name === props.backend)
    ?.models?.find((m) => m.name === props.model);
  const mine = useMemo(
    () => (rows ?? []).filter((r) => r.backend === props.backend && r.model === props.model),
    [rows, props.backend, props.model],
  );
  // Buckets start at the beginning of the period the agent answered for;
  // a bucket without use is a gap in the line.
  const points: Point[] = useMemo(() => {
    if (!doc) return [];
    const byStart = new Map(mine.map((r) => [Date.parse(r.bucket_start ?? ""), r]));
    const out: Point[] = [];
    for (let s = doc.from; s < doc.to; s += b.ms) {
      const r = byStart.get(s);
      out.push({ time: s, value: r ? jpt(r) : null });
    }
    return out;
  }, [doc, mine, b.ms]);
  const total = mine.reduce(
    (a, r) => ({
      energy_j: a.energy_j + r.energy_j,
      generated_tokens: a.generated_tokens + r.generated_tokens,
    }),
    { energy_j: 0, generated_tokens: 0 },
  );
  const tj = jpt(total);
  return (
    <div class="stack">
      <p>
        <a href={href("models")}>{t("gui.models.back")}</a>
      </p>
      {error ? <ErrorNotice error={error} /> : null}
      <div class="grid">
        <Metric label={t("gui.col.energy")} value={fmt.energy(total.energy_j)} tone="load" />
        <Metric
          label={t("gui.col.joules_per_token")}
          value={tj === null ? "-" : fmt.energy(tj)}
          tone="load"
        />
        <Metric
          label={t("gui.col.tokens_per_joule")}
          value={tj === null ? "-" : fmt.decimal(1 / tj, 2)}
          tone="eff"
        />
        <Metric label={t("gui.col.generated_tokens")} value={fmt.count(total.generated_tokens)} />
      </div>
      <Panel title={t("gui.models.history")} help="explanation/attribution">
        {!rows ? (
          <Loading />
        ) : (
          <LineChart
            label={t("gui.col.joules_per_token")}
            points={points}
            tone="load"
            format={fmt.energy}
            formatTime={(ms) => fmt.dateTime(new Date(ms).toISOString())}
          />
        )}
      </Panel>
      {liveModel?.regression ? (
        <Panel title={t("gui.models.regression")} help="explanation/attribution">
          <div class="grid">
            <Metric
              label={t("gui.models.prompt_token")}
              value={fmt.energy(liveModel.regression.prompt_j_per_token)}
              tone="load"
              note={t("gui.models.ci", { value: fmt.energy(liveModel.regression.prompt_ci95) })}
            />
            <Metric
              label={t("gui.models.generated_token")}
              value={fmt.energy(liveModel.regression.generated_j_per_token)}
              tone="load"
              note={t("gui.models.ci", { value: fmt.energy(liveModel.regression.generated_ci95) })}
            />
            <Metric
              label={t("gui.models.base")}
              value={fmt.power(liveModel.regression.base_w)}
              note={t("gui.models.r2", {
                r2: fmt.decimal(liveModel.regression.r2, 2),
                n: liveModel.regression.intervals,
              })}
            />
          </div>
        </Panel>
      ) : null}
      {doc ? <ModelRegressions backend={props.backend} model={props.model} fromMs={doc.from} /> : null}
    </div>
  );
}

export function Models(props: { path: string[] }) {
  const [period, setPeriod] = useState<Period>("7d");
  const [backend, model] = props.path;
  const detail = backend !== undefined && model !== undefined;
  return (
    <div class="stack">
      <div class="page-head">
        <div>
          <h1>{detail ? model : t("gui.models.title")}</h1>
          <p>{detail ? t("gui.models.of", { backend }) : t("gui.models.intro")}</p>
        </div>
        <Segmented
          label={t("gui.period.label")}
          value={period}
          options={periodOptions()}
          onChange={setPeriod}
        />
      </div>
      {detail ? (
        <ModelDetail backend={backend} model={model} period={period} />
      ) : (
        <Panel title={t("gui.models.list")} help="explanation/attribution">
          <ModelList period={period} />
        </Panel>
      )}
    </div>
  );
}
