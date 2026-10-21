// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

import { useEffect, useState } from "preact/hooks";
import { api } from "../api";
import { DataTable } from "../components/table";
import { ErrorNotice, Loading, Metric, Panel } from "../components/ui";
import * as fmt from "../format";
import { t } from "../i18n";
import { selfUse } from "../perf";
import type { Status } from "../types";

interface AboutDoc {
  version: string;
  revision?: string;
  source_url: string;
  modified: boolean;
  copyright: string;
  commercial_contact: string;
  third_party: { name: string; version: string }[];
}

// selfPower estimates the power of the processing of the interface from
// its share of a processor and the energy the node spends per busy
// processor second.
export function selfPower(status: Status): number {
  return selfUse().busyShare * status.cpu_joules_per_busy_second;
}

export function About(props: { status: Status }) {
  const [doc, setDoc] = useState<AboutDoc | null>(null);
  const [error, setError] = useState<unknown>(null);
  const [, tick] = useState(0);
  useEffect(() => {
    api.request<AboutDoc>("GET", "/api/v1/about").then(setDoc).catch(setError);
    const id = window.setInterval(() => tick((n) => n + 1), 10000);
    return () => window.clearInterval(id);
  }, []);
  const use = selfUse();
  const w = selfPower(props.status);
  return (
    <div class="stack">
      <div class="page-head">
        <h1>{t("gui.about.title")}</h1>
      </div>
      {error ? <ErrorNotice error={error} /> : null}
      <Panel title={t("gui.about.self")}>
        <div class="grid">
          <Metric
            label={t("gui.about.self_power")}
            value={fmt.power(w)}
            tone="load"
            note={t("gui.about.self_power_note")}
          />
          <Metric
            label={t("gui.about.self_busy")}
            value={`${fmt.decimal(use.busyShare * 100, 2)} %`}
            note={t("gui.about.self_busy_note", {
              seconds: fmt.decimal(use.busySeconds, 1),
              open: fmt.decimal(use.openSeconds / 60, 0),
            })}
          />
          <Metric
            label={t("gui.about.self_energy")}
            value={fmt.energy(use.busySeconds * props.status.cpu_joules_per_busy_second)}
          />
        </div>
        <p class="muted">{t("gui.about.self_method")}</p>
      </Panel>
      {!doc ? (
        error ? null : (
          <Loading />
        )
      ) : (
        <>
          <Panel title={t("gui.about.license")}>
            <p>
              GreenLens {doc.version}
              <br />
              {doc.copyright}
            </p>
            <p>{t("cli.license.notice")}</p>
            <p>{t("cli.license.network")}</p>
            <p>
              {t("cli.license.source", { url: "" })}
              <a href={doc.source_url} rel="noreferrer">
                {doc.source_url}
              </a>
            </p>
            {doc.modified ? <p class="notice">{t("cli.license.modified")}</p> : null}
            <p>{t("cli.license.commercial", { contact: doc.commercial_contact })}</p>
          </Panel>
          <Panel title={t("cli.license.third_party", { count: doc.third_party.length })}>
            <DataTable
              caption={t("gui.about.third_party_caption")}
              rows={doc.third_party}
              rowKey={(c) => c.name}
              columns={[
                {
                  key: "name",
                  label: t("gui.col.component"),
                  sort: (c) => c.name,
                  cell: (c) => <code>{c.name}</code>,
                },
                { key: "version", label: t("gui.col.version"), cell: (c) => c.version },
              ]}
            />
          </Panel>
        </>
      )}
    </div>
  );
}
