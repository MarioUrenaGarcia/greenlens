// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

import { useCallback, useEffect, useState } from "preact/hooks";
import { api } from "../api";
import { DataTable } from "../components/table";
import { Empty, ErrorNotice, Loading, Metric, Panel } from "../components/ui";
import { csv, save, stamp } from "../download";
import * as fmt from "../format";
import { t } from "../i18n";
import { useLive } from "../live";
import type { Benchmark, BenchmarkJob, BenchmarkRequest, Factors, Role, Status } from "../types";

// The defaults of greenlens bench, so results of both compare.
const defaults = { runs: 5, max_tokens: 128, warmup_s: 30, min_run_s: 60 };

function numberField(
  id: string,
  label: string,
  value: number,
  set: (v: number) => void,
  min: number,
  max: number,
  hint?: string,
) {
  return (
    <div class="field">
      <label for={id}>{label}</label>
      <input
        id={id}
        class="input num"
        type="number"
        min={min}
        max={max}
        value={value}
        onInput={(e) => set(Number((e.target as HTMLInputElement).value))}
        aria-describedby={hint ? `${id}-hint` : undefined}
      />
      {hint ? (
        <span id={`${id}-hint`} class="hint">
          {hint}
        </span>
      ) : null}
    </div>
  );
}

function RunForm(props: { job: BenchmarkJob | null; onStarted: (j: BenchmarkJob) => void }) {
  const live = useLive();
  const servers = (live.backends?.backends ?? []).filter((b) => b.endpoint && b.kind !== "process");
  const [server, setServer] = useState("");
  const [model, setModel] = useState("");
  const [runs, setRuns] = useState(defaults.runs);
  const [maxTokens, setMaxTokens] = useState(defaults.max_tokens);
  const [warmup, setWarmup] = useState(defaults.warmup_s);
  const [minRun, setMinRun] = useState(defaults.min_run_s);
  const [error, setError] = useState<unknown>(null);
  const chosen = servers.find((b) => b.name === server) ?? servers[0];
  const running = props.job?.state === "running";
  const start = async (e: Event) => {
    e.preventDefault();
    if (!chosen) return;
    setError(null);
    const req: BenchmarkRequest = {
      kind: chosen.kind,
      endpoint: chosen.endpoint ?? "",
      model: model.trim(),
      runs,
      max_tokens: maxTokens,
      warmup_s: warmup,
      min_run_s: minRun,
    };
    try {
      props.onStarted(await api.request<BenchmarkJob>("POST", "/api/v1/benchmarks", req));
    } catch (err) {
      setError(err);
    }
  };
  if (servers.length === 0) {
    return <Empty>{t("gui.benchmarks.no_server")}</Empty>;
  }
  return (
    <form onSubmit={start}>
      <div class="grid">
        <div class="field">
          <label for="bench-server">{t("gui.benchmarks.server")}</label>
          <select
            id="bench-server"
            class="input"
            value={chosen?.name}
            onChange={(e) => setServer((e.target as HTMLSelectElement).value)}
          >
            {servers.map((b) => (
              <option key={b.name} value={b.name}>
                {b.name} ({b.endpoint})
              </option>
            ))}
          </select>
        </div>
        <div class="field">
          <label for="bench-model">{t("gui.col.model")}</label>
          <input
            id="bench-model"
            class="input"
            list="bench-models"
            required
            value={model}
            onInput={(e) => setModel((e.target as HTMLInputElement).value)}
          />
          <datalist id="bench-models">
            {(chosen?.models ?? []).map((m) => (
              <option key={m.name} value={m.name} />
            ))}
          </datalist>
        </div>
        {numberField("bench-runs", t("gui.benchmarks.runs"), runs, setRuns, 2, 100)}
        {numberField("bench-tokens", t("gui.benchmarks.max_tokens"), maxTokens, setMaxTokens, 1, 32768)}
        {numberField(
          "bench-warmup",
          t("gui.benchmarks.warmup"),
          warmup,
          setWarmup,
          0,
          600,
          t("gui.benchmarks.warmup_hint"),
        )}
        {numberField(
          "bench-minrun",
          t("gui.benchmarks.min_run"),
          minRun,
          setMinRun,
          0,
          3600,
          t("gui.benchmarks.min_run_hint"),
        )}
      </div>
      {error ? <ErrorNotice error={error} /> : null}
      <button type="submit" class="btn btn-primary" disabled={running || model.trim() === ""}>
        {t("gui.benchmarks.start")}
      </button>
    </form>
  );
}

function Progress(props: { job: BenchmarkJob }) {
  const j = props.job;
  const cancel = () => {
    api.request<BenchmarkJob>("DELETE", "/api/v1/benchmarks/run").catch(() => undefined);
  };
  if (j.state === "idle") return null;
  const label =
    j.state === "running"
      ? t("gui.benchmarks.progress", { run: j.run, runs: j.runs, model: j.request?.model ?? "" })
      : t(`gui.benchmarks.state.${j.state}`, { error: j.error ?? "", model: j.request?.model ?? "" });
  return (
    <div class={`notice ${j.state === "failed" ? "notice-error" : ""}`} role="status">
      <p>{label}</p>
      {j.state === "running" ? (
        <>
          <progress max={j.runs} value={j.run} aria-label={label} style={{ width: "100%" }} />
          <p class="muted">{t("gui.benchmarks.keep_idle")}</p>
          <button type="button" class="btn" onClick={cancel}>
            {t("gui.benchmarks.cancel")}
          </button>
        </>
      ) : null}
    </div>
  );
}

function Compare(props: { a: Benchmark; b: Benchmark; factors: Factors }) {
  const [tokens, setTokens] = useState(1_000_000);
  const a = props.a.j_per_generated_token;
  const b = props.b.j_per_generated_token;
  const savedJ = (a.mean - b.mean) * tokens;
  const kwh = savedJ / fmt.joulesPerKWh;
  const f = props.factors;
  const noise = Math.abs(a.mean - b.mean) <= a.ci95 + b.ci95;
  return (
    <Panel title={t("gui.benchmarks.compare")} level={2} help="guides/bench">
      <p>
        {t("gui.benchmarks.compare_pair", {
          a: `${props.a.target.model} (#${props.a.id})`,
          b: `${props.b.target.model} (#${props.b.id})`,
        })}
      </p>
      {numberField("compare-tokens", t("gui.benchmarks.tokens"), tokens, setTokens, 1, 1e15)}
      <div class="grid">
        <Metric
          label={t("gui.benchmarks.ratio")}
          value={a.mean > 0 ? `${fmt.decimal(b.mean / a.mean, 3)}x` : "-"}
        />
        <Metric
          label={savedJ >= 0 ? t("gui.benchmarks.saved") : t("gui.benchmarks.extra")}
          value={fmt.energy(Math.abs(savedJ))}
          tone={savedJ >= 0 ? "eff" : "load"}
        />
        <Metric
          label={t("gui.summary.co2e")}
          value={fmt.co2e(Math.abs(kwh) * f.emission_g_per_kwh * f.pue)}
          tone={savedJ >= 0 ? "eff" : "load"}
        />
        <Metric
          label={t("gui.summary.cost")}
          value={
            f.tariff_per_kwh === undefined
              ? "-"
              : fmt.money(Math.abs(kwh) * f.tariff_per_kwh * f.pue, f.currency)
          }
          note={f.tariff_per_kwh === undefined ? t("gui.summary.no_tariff") : undefined}
        />
      </div>
      {noise ? <p class="notice">{t("gui.benchmarks.within_noise")}</p> : null}
      {props.a.prompt_set_sha256 !== props.b.prompt_set_sha256 ? (
        <p class="notice">{t("gui.benchmarks.other_prompts")}</p>
      ) : null}
    </Panel>
  );
}

function exportRows(list: Benchmark[]): (string | number | boolean)[][] {
  return [
    [
      "id",
      "started_at",
      "kind",
      "model",
      "basis",
      "calibrated",
      "runs",
      "prompt_set_sha256",
      "generated_tokens",
      "j_per_token_mean",
      "j_per_token_stddev",
      "j_per_token_cv",
      "j_per_token_ci95",
      "energy_j_mean",
    ],
    ...list.map((b) => [
      b.id ?? "",
      b.started_at,
      b.target.kind,
      b.target.model,
      b.basis,
      b.calibrated,
      b.runs.length,
      b.prompt_set_sha256,
      b.generated_tokens,
      b.j_per_generated_token.mean,
      b.j_per_generated_token.stddev,
      b.j_per_generated_token.cv,
      b.j_per_generated_token.ci95,
      b.energy_j.mean,
    ]),
  ];
}

export function Benchmarks(props: { status: Status; role: Role }) {
  const live = useLive();
  const [list, setList] = useState<Benchmark[] | null>(null);
  const [job, setJob] = useState<BenchmarkJob | null>(null);
  const [picked, setPicked] = useState<number[]>([]);
  const [error, setError] = useState<unknown>(null);
  const load = useCallback(() => {
    api
      .request<{ benchmarks: Benchmark[] }>("GET", "/api/v1/benchmarks?limit=1000")
      .then((d) => setList(d.benchmarks))
      .catch(setError);
  }, []);
  useEffect(() => {
    load();
    api.request<BenchmarkJob>("GET", "/api/v1/benchmarks/run").then(setJob).catch(setError);
  }, [load]);
  // The live stream reports progress; a finished benchmark joins the list.
  useEffect(() => {
    if (!live.benchmark) return;
    setJob(live.benchmark);
    if (live.benchmark.state === "done") load();
  }, [live.benchmark, load]);
  const canRun = props.role === "operator" || props.role === "admin";
  const toggle = (id: number) =>
    setPicked((p) => (p.includes(id) ? p.filter((x) => x !== id) : [...p.slice(-1), id]));
  const pair = (list ?? []).filter((b) => b.id !== undefined && picked.includes(b.id));
  return (
    <div class="stack">
      <div class="page-head">
        <div>
          <h1>{t("gui.benchmarks.title")}</h1>
          <p>{t("gui.benchmarks.intro")}</p>
        </div>
      </div>
      {error ? <ErrorNotice error={error} /> : null}
      {job ? <Progress job={job} /> : null}
      {canRun ? (
        <Panel title={t("gui.benchmarks.run")} help="guides/bench">
          <RunForm job={job} onStarted={setJob} />
        </Panel>
      ) : null}
      {pair.length === 2 && pair[0] && pair[1] ? (
        <Compare a={pair[0]} b={pair[1]} factors={props.status.factors} />
      ) : null}
      <Panel
        title={t("gui.benchmarks.stored")}
        help="guides/bench"
        actions={
          list && list.length > 0 ? (
            <>
              <button
                type="button"
                class="btn"
                onClick={() => save(`greenlens-benchmarks-${stamp()}.csv`, "text/csv", csv(exportRows(list)))}
              >
                CSV
              </button>
              <button
                type="button"
                class="btn"
                onClick={() =>
                  save(
                    `greenlens-benchmarks-${stamp()}.json`,
                    "application/json",
                    JSON.stringify({ benchmarks: list }, null, 2),
                  )
                }
              >
                JSON
              </button>
            </>
          ) : null
        }
      >
        {!list ? (
          error ? null : (
            <Loading />
          )
        ) : list.length === 0 ? (
          <Empty>{t("gui.benchmarks.none")}</Empty>
        ) : (
          <>
            <p class="muted">{t("gui.benchmarks.pick")}</p>
            <DataTable<Benchmark>
              caption={t("gui.benchmarks.caption")}
              rows={list}
              rowKey={(b) => String(b.id)}
              columns={[
                {
                  key: "pick",
                  label: t("gui.benchmarks.compare_col"),
                  cell: (b) =>
                    b.id === undefined ? null : (
                      <input
                        type="checkbox"
                        checked={picked.includes(b.id)}
                        onChange={() => b.id !== undefined && toggle(b.id)}
                        aria-label={t("gui.benchmarks.pick_one", { id: b.id })}
                      />
                    ),
                },
                { key: "id", label: "#", numeric: true, sort: (b) => b.id ?? 0, cell: (b) => b.id },
                {
                  key: "date",
                  label: t("gui.col.date"),
                  sort: (b) => b.started_at,
                  cell: (b) => fmt.dateTime(b.started_at),
                },
                {
                  key: "model",
                  label: t("gui.col.model"),
                  sort: (b) => b.target.model,
                  cell: (b) => b.target.model,
                },
                {
                  key: "kind",
                  label: t("gui.col.kind"),
                  sort: (b) => b.target.kind,
                  cell: (b) => b.target.kind,
                },
                { key: "basis", label: t("gui.col.basis"), cell: (b) => t(`gui.basis.${b.basis}`) },
                {
                  key: "jpt",
                  label: t("gui.col.joules_per_token"),
                  numeric: true,
                  sort: (b) => b.j_per_generated_token.mean,
                  cell: (b) =>
                    `${fmt.energy(b.j_per_generated_token.mean)} ± ${fmt.energy(b.j_per_generated_token.ci95)}`,
                },
                {
                  key: "cv",
                  label: t("gui.col.cv"),
                  numeric: true,
                  sort: (b) => b.j_per_generated_token.cv,
                  cell: (b) => (
                    <span
                      class={b.j_per_generated_token.cv > 0.1 ? "load" : ""}
                      title={(b.causes ?? []).map((c) => t(`cli.bench.cause.${c}`)).join("; ")}
                    >
                      {fmt.decimal(b.j_per_generated_token.cv * 100, 1)} %
                    </span>
                  ),
                },
                { key: "runs", label: t("gui.benchmarks.runs"), numeric: true, cell: (b) => b.runs.length },
              ]}
            />
          </>
        )}
      </Panel>
    </div>
  );
}
