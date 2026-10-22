// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

// The alerts: those pending or firing, those that fired in the period, and
// the rules, which an operator edits with a preview of the configuration
// file they produce.

import { useEffect, useState } from "preact/hooks";
import { api } from "../api";
import { DataTable } from "../components/table";
import { Empty, ErrorNotice, Loading, Panel, Segmented } from "../components/ui";
import * as fmt from "../format";
import { t } from "../i18n";
import { useLive } from "../live";
import type { Alert, AlertRule, Alerts } from "../types";
import { AlertState, alertValue, series } from "../components/alert";
import { ConfigResult, type Preview } from "./config";
import { type Period, periodOptions } from "./summary";

const periodMs: Record<Period, number> = { "24h": 864e5, "7d": 7 * 864e5, "30d": 30 * 864e5 };

function iso(d: Date): string {
  return d.toISOString().replace(/\.\d{3}Z$/, "Z");
}

function condition(a: Alert): string {
  return `${t(`gui.alerts.signal.${a.expr}`)} ${a.op} ${alertValue(a.expr, a.threshold)}`;
}

function blankRule(n: number): AlertRule {
  return { name: `rule_${n}`, expr: "node_power_watts", op: ">", threshold: 0, for: "5m" };
}

function RuleEditor(props: {
  rule: AlertRule;
  index: number;
  doc: Alerts;
  errors: Map<string, string>;
  onChange: (r: AlertRule) => void;
  onRemove: () => void;
}) {
  const { rule, index } = props;
  const id = (f: string) => `rule-${index}-${f}`;
  const labels = props.doc.signals.find((s) => s.name === rule.expr)?.labels ?? [];
  const err = (f: string) => props.errors.get(`alerts.rules[${index}]${f ? `.${f}` : ""}`);
  const set = (patch: Partial<AlertRule>) => props.onChange({ ...rule, ...patch });
  const whole = err("");
  return (
    <fieldset class="rule glass glass-2">
      <legend>{t("gui.alerts.rule_n", { n: index + 1 })}</legend>
      {whole ? (
        <p class="notice notice-error" role="alert">
          {whole}
        </p>
      ) : null}
      <div class="rule-fields">
        <label for={id("name")}>
          {t("gui.alerts.field.name")}
          <input
            id={id("name")}
            class="input"
            value={rule.name}
            aria-invalid={Boolean(err("name"))}
            onInput={(e) => set({ name: (e.target as HTMLInputElement).value })}
          />
        </label>
        <label for={id("expr")}>
          {t("gui.alerts.field.expr")}
          <select
            id={id("expr")}
            class="input"
            value={rule.expr}
            onChange={(e) => {
              const expr = (e.target as HTMLSelectElement).value;
              set({ expr, match: {} });
            }}
          >
            {props.doc.signals.map((s) => (
              <option key={s.name} value={s.name}>
                {t(`gui.alerts.signal.${s.name}`)}
              </option>
            ))}
          </select>
        </label>
        <label for={id("op")}>
          {t("gui.alerts.field.op")}
          <select
            id={id("op")}
            class="input"
            value={rule.op}
            onChange={(e) => set({ op: (e.target as HTMLSelectElement).value })}
          >
            {props.doc.operators.map((o) => (
              <option key={o} value={o}>
                {o}
              </option>
            ))}
          </select>
        </label>
        <label for={id("threshold")}>
          {t("gui.alerts.field.threshold")}
          <input
            id={id("threshold")}
            class="input"
            type="number"
            step="any"
            value={rule.threshold}
            onInput={(e) => set({ threshold: Number((e.target as HTMLInputElement).value) })}
          />
        </label>
        <label for={id("for")}>
          {t("gui.alerts.field.for")}
          <input
            id={id("for")}
            class="input"
            value={rule.for}
            aria-invalid={Boolean(err("for"))}
            aria-describedby={`${id("for")}-doc`}
            onInput={(e) => set({ for: (e.target as HTMLInputElement).value })}
          />
          <span id={`${id("for")}-doc`} class="muted">
            {t("gui.alerts.field.for_doc")}
          </span>
        </label>
        {labels.map((l) => (
          <label key={l} for={id(`match-${l}`)}>
            {t("gui.alerts.field.match", { label: l })}
            <input
              id={id(`match-${l}`)}
              class="input"
              value={rule.match?.[l] ?? ""}
              onInput={(e) => {
                const v = (e.target as HTMLInputElement).value;
                const match = { ...(rule.match ?? {}) };
                if (v) match[l] = v;
                else delete match[l];
                set({ match });
              }}
            />
          </label>
        ))}
      </div>
      <button type="button" class="btn" onClick={props.onRemove}>
        {t("gui.alerts.remove", { name: rule.name || String(index + 1) })}
      </button>
    </fieldset>
  );
}

export function AlertsScreen(props: { role: string }) {
  const live = useLive();
  const [period, setPeriod] = useState<Period>("7d");
  const [doc, setDoc] = useState<Alerts | null>(null);
  const [rules, setRules] = useState<AlertRule[] | null>(null);
  const [preview, setPreview] = useState<Preview | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const canEdit = props.role === "operator" || props.role === "admin";

  useEffect(() => {
    const to = new Date();
    const from = new Date(to.getTime() - periodMs[period]);
    let stale = false;
    api
      .request<Alerts>("GET", `/api/v1/alerts?${new URLSearchParams({ from: iso(from), to: iso(to) })}`)
      .then((d) => {
        if (stale) return;
        setDoc(d);
        setRules((r) => r ?? d.rules);
        setError(null);
      })
      .catch((e: unknown) => !stale && setError(e));
    return () => {
      stale = true;
    };
  }, [period, live.alertChanges]);

  const run = async (method: "POST" | "PUT") => {
    if (!rules) return;
    setBusy(true);
    setError(null);
    try {
      const path = method === "POST" ? "/api/v1/alerts/rules/preview" : "/api/v1/alerts/rules";
      const p = await api.request<Preview>(method, path, { rules });
      setPreview(p);
      if (p.applied) setRules(null);
    } catch (e) {
      setError(e);
    } finally {
      setBusy(false);
    }
  };

  if (error && !doc) return <ErrorNotice error={error} />;
  if (!doc) return <Loading />;
  const errors = new Map((preview?.errors ?? []).filter((e) => e.key).map((e) => [e.key ?? "", e.message]));
  const edited = rules ?? doc.rules;
  const changed = JSON.stringify(edited) !== JSON.stringify(doc.rules);
  return (
    <div class="stack">
      <div class="page-head">
        <div>
          <h1>{t("gui.alerts.title")}</h1>
          <p>
            {t("gui.alerts.intro", { seconds: doc.every_s })}{" "}
            {doc.sinks.length > 0
              ? t("gui.alerts.sinks", { sinks: doc.sinks.join(", ") })
              : t("gui.alerts.no_sinks")}
          </p>
        </div>
        <Segmented
          label={t("gui.period.label")}
          value={period}
          options={periodOptions()}
          onChange={setPeriod}
        />
      </div>
      {error ? <ErrorNotice error={error} /> : null}
      <Panel title={t("gui.alerts.active")} help="guides/alerts">
        {doc.active.length === 0 ? (
          <Empty>{doc.rules.length === 0 ? t("gui.alerts.no_rules") : t("gui.alerts.none_active")}</Empty>
        ) : (
          <DataTable<Alert>
            caption={t("gui.alerts.active_caption")}
            rows={doc.active}
            rowKey={(a) => `${a.rule}/${series(a.labels)}`}
            columns={[
              {
                key: "state",
                label: t("gui.alerts.col.state"),
                sort: (a) => a.state,
                cell: (a) => <AlertState state={a.state} />,
              },
              { key: "rule", label: t("gui.alerts.col.rule"), sort: (a) => a.rule, cell: (a) => a.rule },
              {
                key: "series",
                label: t("gui.alerts.col.series"),
                sort: (a) => series(a.labels),
                cell: (a) => series(a.labels),
              },
              { key: "condition", label: t("gui.alerts.col.condition"), cell: condition },
              {
                key: "value",
                label: t("gui.alerts.col.value"),
                numeric: true,
                sort: (a) => a.value,
                cell: (a) => alertValue(a.expr, a.value),
              },
              {
                key: "since",
                label: t("gui.alerts.col.since"),
                sort: (a) => a.fired_at ?? a.started_at,
                cell: (a) => fmt.dateTime(a.fired_at ?? a.started_at),
              },
            ]}
          />
        )}
      </Panel>
      <Panel title={t("gui.alerts.history")} help="guides/alerts">
        {doc.history.length === 0 ? (
          <Empty>{t("gui.alerts.no_history")}</Empty>
        ) : (
          <DataTable<Alert>
            caption={t("gui.alerts.history_caption")}
            rows={doc.history}
            rowKey={(a) => String(a.id)}
            initial={{ key: "fired", desc: true }}
            columns={[
              { key: "rule", label: t("gui.alerts.col.rule"), sort: (a) => a.rule, cell: (a) => a.rule },
              {
                key: "series",
                label: t("gui.alerts.col.series"),
                sort: (a) => series(a.labels),
                cell: (a) => series(a.labels),
              },
              { key: "condition", label: t("gui.alerts.col.condition"), cell: condition },
              {
                key: "fired",
                label: t("gui.alerts.col.fired"),
                sort: (a) => a.fired_at ?? "",
                cell: (a) => (a.fired_at ? fmt.dateTime(a.fired_at) : "-"),
              },
              {
                key: "resolved",
                label: t("gui.alerts.col.resolved"),
                sort: (a) => a.resolved_at ?? "",
                cell: (a) => (a.resolved_at ? fmt.dateTime(a.resolved_at) : <AlertState state={a.state} />),
              },
            ]}
          />
        )}
      </Panel>
      <Panel
        title={t("gui.alerts.rules")}
        help="guides/alerts"
        actions={
          canEdit ? (
            <>
              <button
                type="button"
                class="btn"
                onClick={() => setRules([...edited, blankRule(edited.length + 1)])}
              >
                {t("gui.alerts.add")}
              </button>
              <button type="button" class="btn" disabled={busy || !changed} onClick={() => run("POST")}>
                {t("gui.alerts.preview")}
              </button>
              {doc.config_writable ? (
                <button
                  type="button"
                  class="btn btn-primary"
                  disabled={busy || !changed}
                  onClick={() => run("PUT")}
                >
                  {t("gui.config.apply")}
                </button>
              ) : null}
            </>
          ) : null
        }
      >
        {!canEdit ? <p class="muted">{t("gui.alerts.read_only_role")}</p> : null}
        {canEdit && !doc.config_writable ? <p class="notice">{t("gui.config.read_only")}</p> : null}
        {edited.length === 0 ? (
          <Empty>{t("gui.alerts.no_rules")}</Empty>
        ) : canEdit ? (
          <div class="stack">
            {edited.map((r, i) => (
              <RuleEditor
                // Rules are edited in place, so their position is their identity.
                key={i}
                rule={r}
                index={i}
                doc={doc}
                errors={errors}
                onChange={(nr) => setRules(edited.map((x, j) => (j === i ? nr : x)))}
                onRemove={() => setRules(edited.filter((_, j) => j !== i))}
              />
            ))}
          </div>
        ) : (
          <ul class="rules">
            {edited.map((r) => (
              <li key={r.name}>
                <strong>{r.name}</strong>: {t(`gui.alerts.signal.${r.expr}`)} {r.op}{" "}
                {alertValue(r.expr, r.threshold)}
                {r.for && r.for !== "0s" ? ` ${t("gui.alerts.for", { duration: r.for })}` : ""}
              </li>
            ))}
          </ul>
        )}
      </Panel>
      {preview ? (
        <ConfigResult preview={preview} writable={doc.config_writable} help="guides/alerts" />
      ) : null}
    </div>
  );
}
