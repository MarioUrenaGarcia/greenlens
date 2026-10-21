// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

import type { ComponentChildren } from "preact";
import { href } from "../router";
import { t } from "../i18n";
import { icons } from "./icons";

// Panel is a pane of glass with a heading. help names the page of the
// embedded documentation that explains it.
export function Panel(props: {
  title: string;
  level?: 1 | 2 | 3;
  help?: string;
  actions?: ComponentChildren;
  class?: string;
  children: ComponentChildren;
}) {
  const level = props.level ?? 1;
  const cls = `glass${level > 1 ? ` glass-${level}` : ""} panel ${props.class ?? ""}`;
  return (
    <section class={cls} aria-label={props.title}>
      <div class="panel-head">
        <h2>{props.title}</h2>
        <div class="panel-actions">
          {props.actions}
          {props.help ? (
            <a
              class="btn btn-icon"
              href={href("help", ...props.help.split("/"))}
              title={t("gui.help.about", { topic: props.title })}
            >
              {icons.help()}
              <span class="visually-hidden">{t("gui.help.about", { topic: props.title })}</span>
            </a>
          ) : null}
        </div>
      </div>
      {props.children}
    </section>
  );
}

export type Tone = "load" | "eff" | "critical" | "plain";

export function Metric(props: { label: string; value: string; note?: string | undefined; tone?: Tone }) {
  const tone = props.tone && props.tone !== "plain" ? props.tone : "";
  return (
    <div class="inner metric">
      <p class="metric-label">{props.label}</p>
      <p class={`metric-value ${tone}`}>{props.value}</p>
      {props.note ? <p class="metric-note">{props.note}</p> : null}
    </div>
  );
}

export function Empty(props: { children: ComponentChildren }) {
  return <div class="empty">{props.children}</div>;
}

export function ErrorNotice(props: { error: unknown }) {
  const msg = props.error instanceof Error ? props.error.message : String(props.error);
  return (
    <div class="notice notice-error" role="alert">
      <strong>{t("gui.error.title")}</strong> {msg}
    </div>
  );
}

export function Loading() {
  return (
    <p class="muted" role="status">
      {t("gui.loading")}
    </p>
  );
}

// Segmented is a group of mutually exclusive buttons.
export function Segmented<T extends string>(props: {
  label: string;
  value: T;
  options: { value: T; label: string }[];
  onChange: (v: T) => void;
}) {
  return (
    <fieldset class="segmented">
      <legend class="visually-hidden">{props.label}</legend>
      {props.options.map((o) => (
        <button
          type="button"
          key={o.value}
          aria-pressed={o.value === props.value}
          onClick={() => props.onChange(o.value)}
        >
          {o.label}
        </button>
      ))}
    </fieldset>
  );
}
