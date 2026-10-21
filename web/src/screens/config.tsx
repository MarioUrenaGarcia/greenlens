// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

// The configuration, edited key by key with a preview of the resulting
// file. The agent applies it only when it can write its own file; the
// system service gets the file to install instead.

import { useEffect, useMemo, useState } from "preact/hooks";
import { api } from "../api";
import { ErrorNotice, Loading, Panel } from "../components/ui";
import { save } from "../download";
import { has, t } from "../i18n";

interface Field {
  key: string;
  type: string;
  value: string;
  default: string;
  env?: string;
  enum?: string[];
}

interface ConfigDoc {
  path: string;
  writable: boolean;
  reason?: "no_file" | "read_only";
  yaml: string;
  fields: Field[];
  restart_keys: string[];
}

interface Preview {
  yaml: string;
  valid: boolean;
  errors: { key?: string; message: string }[];
  needs_restart: boolean;
  applied: boolean;
}

export function docKey(key: string): string {
  return `config.doc.${key.replace(/\[\]/g, "").replace(/\./g, "_")}`;
}

function needsRestart(key: string, keys: string[]): boolean {
  return keys.some((k) => key === k || (k.endsWith(".") && key.startsWith(k)));
}

function Input(props: { f: Field; value: string; onChange: (v: string) => void; invalid: boolean }) {
  const { f } = props;
  const id = `cfg-${f.key}`;
  const common = { id, class: "input", "aria-invalid": props.invalid, "aria-describedby": `${id}-doc` };
  if (f.type === "bool") {
    return (
      <select
        {...common}
        value={props.value}
        onChange={(e) => props.onChange((e.target as HTMLSelectElement).value)}
      >
        <option value="true">{t("gui.yes")}</option>
        <option value="false">{t("gui.no")}</option>
      </select>
    );
  }
  if (f.enum && f.type === "string") {
    return (
      <select
        {...common}
        value={props.value}
        onChange={(e) => props.onChange((e.target as HTMLSelectElement).value)}
      >
        {f.enum.map((v) => (
          <option key={v} value={v}>
            {v}
          </option>
        ))}
      </select>
    );
  }
  return (
    <input
      {...common}
      type="text"
      spellcheck={false}
      value={props.value}
      onInput={(e) => props.onChange((e.target as HTMLInputElement).value)}
    />
  );
}

export function Config() {
  const [doc, setDoc] = useState<ConfigDoc | null>(null);
  const [edits, setEdits] = useState<Record<string, string>>({});
  const [preview, setPreview] = useState<Preview | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const load = () => {
    api
      .request<ConfigDoc>("GET", "/api/v1/config")
      .then((d) => {
        setDoc(d);
        setEdits({});
      })
      .catch(setError);
  };
  useEffect(load, []);
  const sections = useMemo(() => {
    const out = new Map<string, Field[]>();
    for (const f of doc?.fields ?? []) {
      const s = f.key.split(".")[0] ?? "";
      out.set(s, [...(out.get(s) ?? []), f]);
    }
    return out;
  }, [doc]);
  const changes = useMemo(() => {
    const out: Record<string, string> = {};
    for (const f of doc?.fields ?? []) {
      const v = edits[f.key];
      if (v !== undefined && v !== f.value) out[f.key] = v;
    }
    return out;
  }, [doc, edits]);
  const changed = Object.keys(changes).length;
  const errorsByKey = new Map(
    (preview?.errors ?? []).filter((e) => e.key).map((e) => [e.key ?? "", e.message]),
  );

  const run = async (method: "POST" | "PUT") => {
    setBusy(true);
    setError(null);
    try {
      const path = method === "POST" ? "/api/v1/config/preview" : "/api/v1/config";
      const p = await api.request<Preview>(method, path, { changes });
      setPreview(p);
      if (p.applied) load();
    } catch (e) {
      setError(e);
    } finally {
      setBusy(false);
    }
  };

  if (error && !doc) return <ErrorNotice error={error} />;
  if (!doc) return <Loading />;
  return (
    <div class="stack">
      <div class="page-head">
        <div>
          <h1>{t("gui.config.title")}</h1>
          <p>{doc.path ? t("gui.config.file", { path: doc.path }) : t("gui.config.no_file")}</p>
        </div>
        <div class="panel-actions">
          <button type="button" class="btn" disabled={busy || changed === 0} onClick={() => run("POST")}>
            {t("gui.config.preview", { count: changed })}
          </button>
          {doc.writable ? (
            <button
              type="button"
              class="btn btn-primary"
              disabled={busy || changed === 0}
              onClick={() => run("PUT")}
            >
              {t("gui.config.apply")}
            </button>
          ) : null}
        </div>
      </div>
      {!doc.writable ? (
        <p class="notice">
          {t(doc.reason === "no_file" ? "gui.config.read_only_no_file" : "gui.config.read_only")}
        </p>
      ) : null}
      {error ? <ErrorNotice error={error} /> : null}
      {preview ? (
        <Panel
          title={preview.applied ? t("gui.config.applied") : t("gui.config.result")}
          level={2}
          help="reference/configuration"
        >
          {preview.errors.length > 0 ? (
            <div class="notice notice-error" role="alert">
              <strong>{t("gui.config.invalid")}</strong>
              <ul>
                {preview.errors.map((e) => (
                  <li key={`${e.key}-${e.message}`}>
                    {e.key ? <code>{e.key}</code> : null} {e.message}
                  </li>
                ))}
              </ul>
            </div>
          ) : (
            <p role="status">{preview.applied ? t("gui.config.applied_note") : t("gui.config.valid")}</p>
          )}
          {preview.needs_restart ? <p class="notice">{t("gui.config.restart")}</p> : null}
          {preview.yaml ? (
            <>
              <pre class="inner" style={{ padding: "var(--space-3)", overflowX: "auto" }}>
                <code>{preview.yaml}</code>
              </pre>
              {!doc.writable && preview.valid ? (
                <>
                  <button
                    type="button"
                    class="btn"
                    onClick={() => save("greenlens.yaml", "application/yaml", preview.yaml)}
                  >
                    {t("gui.config.download")}
                  </button>
                  <p>{t("gui.config.install")}</p>
                  <pre class="inner" style={{ padding: "var(--space-3)", overflowX: "auto" }}>
                    <code>
                      {
                        "sudo install -m 0644 greenlens.yaml /etc/greenlens/greenlens.yaml\nsudo systemctl reload greenlens"
                      }
                    </code>
                  </pre>
                </>
              ) : null}
            </>
          ) : null}
        </Panel>
      ) : null}
      {[...sections.entries()].map(([section, fields]) => (
        <details key={section} class="glass panel" open={fields.some((f) => f.key in changes)}>
          <summary>
            <h2 style={{ display: "inline" }}>{section}</h2>{" "}
            <span class="muted">{has(docKey(section)) ? t(docKey(section)) : ""}</span>
          </summary>
          <div class="stack" style={{ marginTop: "var(--space-4)" }}>
            {fields.map((f) => {
              const value = edits[f.key] ?? f.value;
              const bad = errorsByKey.get(f.key);
              return (
                <div key={f.key} class="field">
                  <label for={`cfg-${f.key}`}>
                    <code>{f.key}</code>{" "}
                    {f.key in changes ? <span class="badge">{t("gui.config.changed")}</span> : null}{" "}
                    {needsRestart(f.key, doc.restart_keys) ? (
                      <span class="badge">{t("gui.config.restart_badge")}</span>
                    ) : null}
                  </label>
                  <Input
                    f={f}
                    value={value}
                    invalid={bad !== undefined}
                    onChange={(v) => setEdits({ ...edits, [f.key]: v })}
                  />
                  <span id={`cfg-${f.key}-doc`} class="hint">
                    {t(docKey(f.key))} {t("gui.config.default", { value: f.default })}
                    {f.env ? ` ${t("gui.config.env", { env: f.env })}` : ""}
                  </span>
                  {bad ? <span class="hint critical">{bad}</span> : null}
                </div>
              );
            })}
          </div>
        </details>
      ))}
    </div>
  );
}
