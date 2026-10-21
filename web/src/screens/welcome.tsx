// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

// The first steps: what the agent measures and how well, the idle
// calibration and the factors that turn energy into emissions and cost.
// Every step can be skipped.

import { useEffect, useState } from "preact/hooks";
import { api } from "../api";
import { ErrorNotice, Metric, Panel } from "../components/ui";
import { save } from "../download";
import * as fmt from "../format";
import { t } from "../i18n";
import { useLive } from "../live";
import { prefs } from "../prefs";
import { href } from "../router";
import type { CalibrationJob, Role, Status } from "../types";

const steps = ["sources", "calibration", "factors", "done"] as const;
type Step = (typeof steps)[number];

function SourcesStep(props: { status: Status }) {
  const used = props.status.channels.filter((c) => c.selected);
  return (
    <>
      <p>{t("gui.welcome.sources_intro")}</p>
      <ul class="problems">
        {used.map((c) => (
          <li key={c.id} class="inner notice">
            <strong>{t(`domain.${c.domain}`)}</strong> <span class="badge">{t(`quality.${c.quality}`)}</span>{" "}
            <code>{c.source}</code>
          </li>
        ))}
      </ul>
      <p class="muted">{t("explain.quality.formula")}</p>
      {props.status.estimated ? <p class="notice">{t("gui.welcome.estimated")}</p> : null}
      {(props.status.problems?.length ?? 0) > 0 ? (
        <p class="notice">
          {t("gui.summary.problems", { count: props.status.problems?.length ?? 0 })}{" "}
          <a href={href("sources")}>{t("gui.summary.see_sources")}</a>
        </p>
      ) : null}
    </>
  );
}

function CalibrationStep(props: { status: Status; role: Role }) {
  const live = useLive();
  const [job, setJob] = useState<CalibrationJob | null>(null);
  const [error, setError] = useState<unknown>(null);
  const [now, setNow] = useState(Date.now());
  useEffect(() => {
    api.request<CalibrationJob>("GET", "/api/v1/calibrations/run").then(setJob).catch(setError);
    const id = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(id);
  }, []);
  useEffect(() => {
    if (live.calibration) setJob(live.calibration);
  }, [live.calibration]);
  const canRun = props.role !== "viewer";
  const running = job?.state === "running";
  const left =
    running && job?.started_at
      ? Math.max(0, Math.ceil(job.duration_s - (now - Date.parse(job.started_at)) / 1000))
      : 0;
  const start = () => {
    setError(null);
    api
      .request<CalibrationJob>("POST", "/api/v1/calibrations/run", { duration_s: 60 })
      .then(setJob)
      .catch(setError);
  };
  const cal = props.status.calibration;
  return (
    <>
      <p>{t("gui.welcome.calibration_intro")}</p>
      {cal ? (
        <p class="notice">
          {t("gui.welcome.calibrated", { power: fmt.power(cal.baseline.cpu_w), date: fmt.dateTime(cal.at) })}
        </p>
      ) : null}
      {error ? <ErrorNotice error={error} /> : null}
      {running ? (
        <div class="notice" role="status">
          <p>{t("gui.welcome.measuring", { seconds: left })}</p>
          <progress
            max={job?.duration_s ?? 60}
            value={(job?.duration_s ?? 60) - left}
            aria-label={t("gui.welcome.measuring", { seconds: left })}
          />
        </div>
      ) : job?.state === "done" && job.result ? (
        <div class="grid">
          {Object.entries(job.result.domains).map(([d, s]) => (
            <Metric
              key={d}
              label={t(`domain.${d}`)}
              value={fmt.power(s.p50_w)}
              tone="load"
              note={t("gui.welcome.range", { p10: fmt.power(s.p10_w), p90: fmt.power(s.p90_w) })}
            />
          ))}
        </div>
      ) : job?.state === "failed" ? (
        <p class="notice notice-error">{job.error}</p>
      ) : null}
      {canRun ? (
        <button type="button" class="btn btn-primary" disabled={running} onClick={start}>
          {t("gui.welcome.calibrate")}
        </button>
      ) : (
        <p class="muted">{t("gui.welcome.needs_operator")}</p>
      )}
    </>
  );
}

function FactorsStep(props: { status: Status; role: Role }) {
  const f = props.status.factors;
  const [factor, setFactor] = useState(String(f.emission_g_per_kwh));
  const [source, setSource] = useState(f.emission_source);
  const [price, setPrice] = useState(f.tariff_per_kwh === undefined ? "" : String(f.tariff_per_kwh));
  const [currency, setCurrency] = useState(f.currency);
  const [result, setResult] = useState<{
    yaml: string;
    valid: boolean;
    applied: boolean;
    errors: { key?: string; message: string }[];
  } | null>(null);
  const [writable, setWritable] = useState<boolean | null>(null);
  const [error, setError] = useState<unknown>(null);
  const admin = props.role === "admin";
  useEffect(() => {
    if (!admin) return;
    api
      .request<{ writable: boolean }>("GET", "/api/v1/config")
      .then((d) => setWritable(d.writable))
      .catch(setError);
  }, [admin]);
  const changes = () => {
    const c: Record<string, string> = {};
    if (factor !== String(f.emission_g_per_kwh) || source !== f.emission_source) {
      c["indicators.emission_factor_g_per_kwh"] = factor;
      c["indicators.emission_factor_source"] = source;
    }
    if (price !== (f.tariff_per_kwh === undefined ? "" : String(f.tariff_per_kwh)))
      c["indicators.tariff.price_per_kwh"] = price;
    if (currency !== f.currency) c["indicators.tariff.currency"] = currency;
    return c;
  };
  const submit = (e: Event) => {
    e.preventDefault();
    setError(null);
    api
      .request<typeof result & object>(
        writable ? "PUT" : "POST",
        writable ? "/api/v1/config" : "/api/v1/config/preview",
        { changes: changes() },
      )
      .then(setResult)
      .catch(setError);
  };
  return (
    <>
      <p>{t("gui.welcome.factors_intro")}</p>
      <form onSubmit={submit}>
        <div class="grid">
          <div class="field">
            <label for="w-factor">{t("gui.welcome.factor")}</label>
            <input
              id="w-factor"
              class="input num"
              type="number"
              min="0"
              step="any"
              value={factor}
              disabled={!admin}
              onInput={(e) => setFactor((e.target as HTMLInputElement).value)}
            />
          </div>
          <div class="field">
            <label for="w-source">{t("gui.welcome.factor_source")}</label>
            <input
              id="w-source"
              class="input"
              value={source}
              disabled={!admin}
              onInput={(e) => setSource((e.target as HTMLInputElement).value)}
            />
          </div>
          <div class="field">
            <label for="w-price">{t("gui.welcome.price")}</label>
            <input
              id="w-price"
              class="input num"
              type="number"
              min="0"
              step="any"
              value={price}
              disabled={!admin}
              onInput={(e) => setPrice((e.target as HTMLInputElement).value)}
            />
          </div>
          <div class="field">
            <label for="w-currency">{t("gui.welcome.currency")}</label>
            <input
              id="w-currency"
              class="input"
              maxLength={3}
              value={currency}
              disabled={!admin}
              onInput={(e) => setCurrency((e.target as HTMLInputElement).value.toUpperCase())}
            />
          </div>
        </div>
        {admin ? (
          <button type="submit" class="btn btn-primary" disabled={Object.keys(changes()).length === 0}>
            {writable ? t("gui.config.apply") : t("gui.welcome.prepare")}
          </button>
        ) : (
          <p class="muted">{t("gui.welcome.needs_admin")}</p>
        )}
      </form>
      {error ? <ErrorNotice error={error} /> : null}
      {result ? (
        result.errors.length > 0 ? (
          <div class="notice notice-error" role="alert">
            {result.errors.map((e) => (
              <p key={e.message}>
                {e.key ? <code>{e.key}</code> : null} {e.message}
              </p>
            ))}
          </div>
        ) : result.applied ? (
          <p class="notice" role="status">
            {t("gui.config.applied_note")}
          </p>
        ) : (
          <div class="notice" role="status">
            <p>{t("gui.welcome.install")}</p>
            <button
              type="button"
              class="btn"
              onClick={() => save("greenlens.yaml", "application/yaml", result.yaml)}
            >
              {t("gui.config.download")}
            </button>
            <pre class="inner" style={{ padding: "var(--space-3)", overflowX: "auto" }}>
              <code>
                {
                  "sudo install -m 0644 greenlens.yaml /etc/greenlens/greenlens.yaml\nsudo systemctl reload greenlens"
                }
              </code>
            </pre>
          </div>
        )
      ) : null}
    </>
  );
}

export function Welcome(props: { status: Status; role: Role }) {
  const [step, setStep] = useState<Step>("sources");
  const i = steps.indexOf(step);
  const finish = () => {
    prefs.setWelcomed();
    location.hash = href();
  };
  return (
    <div class="stack">
      <div class="page-head">
        <div>
          <h1>{t("gui.welcome.title")}</h1>
          <p>{t("gui.welcome.step", { n: i + 1, total: steps.length })}</p>
        </div>
        <button type="button" class="btn" onClick={finish}>
          {t("gui.welcome.skip_all")}
        </button>
      </div>
      <Panel
        title={t(`gui.welcome.${step}`)}
        level={2}
        help={
          step === "calibration"
            ? "guides/agent"
            : step === "factors"
              ? "reference/configuration"
              : "explanation/measurement"
        }
      >
        {step === "sources" ? <SourcesStep status={props.status} /> : null}
        {step === "calibration" ? <CalibrationStep status={props.status} role={props.role} /> : null}
        {step === "factors" ? <FactorsStep status={props.status} role={props.role} /> : null}
        {step === "done" ? <p>{t("gui.welcome.done_intro")}</p> : null}
      </Panel>
      <div class="panel-actions">
        {i > 0 ? (
          <button type="button" class="btn" onClick={() => setStep(steps[i - 1] ?? "sources")}>
            {t("gui.welcome.back")}
          </button>
        ) : null}
        {step === "done" ? (
          <button type="button" class="btn btn-primary" onClick={finish}>
            {t("gui.welcome.finish")}
          </button>
        ) : (
          <button type="button" class="btn btn-primary" onClick={() => setStep(steps[i + 1] ?? "done")}>
            {i === steps.length - 2 ? t("gui.welcome.next_last") : t("gui.welcome.next")}
          </button>
        )}
      </div>
    </div>
  );
}
