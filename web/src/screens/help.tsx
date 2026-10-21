// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

// The documentation embedded in the agent, with its index and a search.

import { useEffect, useRef, useState } from "preact/hooks";
import { api } from "../api";
import { ErrorNotice, Loading } from "../components/ui";
import { lang, t } from "../i18n";
import { href } from "../router";

interface PageInfo {
  path: string;
  section: string;
  title: string;
  description?: string;
}

interface Index {
  sections: string[];
  pages: PageInfo[];
}

interface Page {
  path: string;
  title: string;
  html: string;
}

interface SearchResult {
  path: string;
  title: string;
  snippet: string;
}

function HelpIndex(props: { index: Index }) {
  return (
    <nav aria-label={t("gui.help.index")}>
      {props.index.sections.map((s) => {
        const pages = props.index.pages.filter((p) => p.section === s && p.path !== s);
        const head = props.index.pages.find((p) => p.path === s);
        return (
          <section key={s} class="help-section">
            <h2>{head?.title ?? s}</h2>
            <ul>
              {pages.map((p) => (
                <li key={p.path}>
                  <a href={href("help", ...p.path.split("/"))}>{p.title}</a>
                  {p.description ? <span class="muted"> {p.description}</span> : null}
                </li>
              ))}
            </ul>
          </section>
        );
      })}
    </nav>
  );
}

function Search() {
  const [q, setQ] = useState("");
  const [results, setResults] = useState<SearchResult[] | null>(null);
  const [error, setError] = useState<unknown>(null);
  const submit = (e: Event) => {
    e.preventDefault();
    if (q.trim() === "") return;
    const query = new URLSearchParams({ q: q.trim(), lang: lang() });
    api
      .request<{ results: SearchResult[] }>("GET", `/api/v1/help?${query}`)
      .then((r) => setResults(r.results))
      .catch(setError);
  };
  return (
    <div class="stack">
      <search>
        <form onSubmit={submit} class="panel-actions">
          <label for="help-q" class="visually-hidden">
            {t("gui.help.search")}
          </label>
          <input
            id="help-q"
            class="input"
            type="search"
            value={q}
            maxLength={200}
            placeholder={t("gui.help.search")}
            onInput={(e) => setQ((e.target as HTMLInputElement).value)}
          />
          <button type="submit" class="btn">
            {t("gui.help.search_button")}
          </button>
        </form>
      </search>
      {error ? <ErrorNotice error={error} /> : null}
      {results ? (
        <div role="status">
          {results.length === 0 ? (
            <p class="muted">{t("gui.help.no_results", { query: q })}</p>
          ) : (
            <ul class="problems">
              {results.map((r) => (
                <li key={r.path} class="inner notice">
                  <a href={href("help", ...r.path.split("/"))}>
                    <strong>{r.title}</strong>
                  </a>
                  <p class="muted">{r.snippet}</p>
                </li>
              ))}
            </ul>
          )}
        </div>
      ) : null}
    </div>
  );
}

function HelpPage(props: { path: string; anchor: string | null }) {
  const [page, setPage] = useState<Page | null>(null);
  const [error, setError] = useState<unknown>(null);
  const body = useRef<HTMLDivElement>(null);
  useEffect(() => {
    let stale = false;
    setPage(null);
    setError(null);
    api
      .request<Page>(
        "GET",
        `/api/v1/help/${props.path.split("/").map(encodeURIComponent).join("/")}?lang=${lang()}`,
      )
      .then((p) => !stale && setPage(p))
      .catch((e: unknown) => !stale && setError(e));
    return () => {
      stale = true;
    };
  }, [props.path]);
  useEffect(() => {
    if (!page || !body.current) return;
    const target = props.anchor ? document.getElementById(props.anchor) : null;
    if (target) {
      target.scrollIntoView();
      target.setAttribute("tabindex", "-1");
      target.focus();
    } else {
      window.scrollTo(0, 0);
    }
  }, [page, props.anchor]);
  if (error) return <ErrorNotice error={error} />;
  if (!page) return <Loading />;
  return (
    <article class="glass panel doc">
      <p class="no-print">
        <a href={href("help")}>{t("gui.help.all")}</a>
      </p>
      <h1>{page.title}</h1>
      {/* The agent renders its own documentation without raw HTML. */}
      <div ref={body} class="doc-body" dangerouslySetInnerHTML={{ __html: page.html }} />
    </article>
  );
}

export function Help(props: { path: string[]; anchor: string | null }) {
  const [index, setIndex] = useState<Index | null>(null);
  const [error, setError] = useState<unknown>(null);
  useEffect(() => {
    api.request<Index>("GET", `/api/v1/help?lang=${lang()}`).then(setIndex).catch(setError);
  }, []);
  if (props.path.length > 0) {
    return <HelpPage path={props.path.join("/")} anchor={props.anchor} />;
  }
  return (
    <div class="stack">
      <div class="page-head">
        <div>
          <h1>{t("gui.help.title")}</h1>
          <p>{t("gui.help.intro")}</p>
        </div>
      </div>
      <div class="glass panel">
        <Search />
      </div>
      {error ? <ErrorNotice error={error} /> : null}
      {!index ? (
        error ? null : (
          <Loading />
        )
      ) : (
        <div class="glass panel">
          <HelpIndex index={index} />
        </div>
      )}
    </div>
  );
}
