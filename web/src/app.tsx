// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

import type { ComponentChildren } from "preact";
import { useCallback, useEffect, useState } from "preact/hooks";
import { ApiError, api, setCsrf } from "./api";
import { icons } from "./components/icons";
import { ErrorNotice, Loading } from "./components/ui";
import { languages, loadCatalog, lang, pickLanguage, t } from "./i18n";
import { setLiveThrottle } from "./live";
import {
  applyEfficient,
  applyTheme,
  onSystemTheme,
  prefs,
  type EfficientChoice,
  type ThemeChoice,
} from "./prefs";
import { href, useRoute } from "./router";
import { About, selfPower } from "./screens/about";
import { BackendsScreen } from "./screens/backends";
import { Benchmarks } from "./screens/benchmarks";
import { Config } from "./screens/config";
import { Consumers } from "./screens/consumers";
import { Help } from "./screens/help";
import { Login } from "./screens/login";
import { Models } from "./screens/models";
import { Reports } from "./screens/reports";
import { Sources } from "./screens/sources";
import { Summary } from "./screens/summary";
import { Welcome } from "./screens/welcome";
import type { Session, Status } from "./types";

interface Screen {
  id: string;
  icon: () => ComponentChildren;
  // admin screens are listed only for administrators.
  admin?: boolean;
  // hidden screens are reached by links, not from the navigation.
  hidden?: boolean;
  render: (ctx: {
    status: Status;
    session: Session;
    path: string[];
    query: URLSearchParams;
  }) => ComponentChildren;
}

const screens: Screen[] = [
  { id: "summary", icon: icons.summary, render: ({ status }) => <Summary status={status} /> },
  { id: "models", icon: icons.models, render: ({ path }) => <Models path={path} /> },
  { id: "backends", icon: icons.backends, render: () => <BackendsScreen /> },
  { id: "consumers", icon: icons.consumers, render: () => <Consumers /> },
  { id: "reports", icon: icons.reports, render: ({ status }) => <Reports status={status} /> },
  {
    id: "benchmarks",
    icon: icons.benchmarks,
    render: ({ status, session }) => <Benchmarks status={status} role={session.role} />,
  },
  { id: "sources", icon: icons.sources, render: ({ status }) => <Sources status={status} /> },
  { id: "config", icon: icons.config, admin: true, render: () => <Config /> },
  {
    id: "help",
    icon: icons.help,
    render: ({ path, query }) => <Help path={path} anchor={query.get("anchor")} />,
  },
  { id: "about", icon: icons.about, render: ({ status }) => <About status={status} /> },
  {
    id: "welcome",
    icon: icons.help,
    hidden: true,
    render: ({ status, session }) => <Welcome status={status} role={session.role} />,
  },
];

// selfLimitW is the estimated power of the interface above which the
// automatic efficient mode turns on.
const selfLimitW = 1;

interface Battery {
  charging: boolean;
  addEventListener: (type: "chargingchange", f: () => void) => void;
}

// useEfficient decides whether the efficient mode is on: by the choice of
// the user, or, in automatic mode, while this device runs on battery or the
// interface itself is estimated to draw more than selfLimitW.
function useEfficient(choice: EfficientChoice, status: Status): boolean {
  const [onBattery, setOnBattery] = useState(false);
  const [heavy, setHeavy] = useState(false);
  useEffect(() => {
    const id = window.setInterval(() => setHeavy(selfPower(status) > selfLimitW), 10000);
    return () => window.clearInterval(id);
  }, [status]);
  useEffect(() => {
    const nav = navigator as Navigator & { getBattery?: () => Promise<Battery> };
    nav
      .getBattery?.()
      .then((b) => {
        setOnBattery(!b.charging);
        b.addEventListener("chargingchange", () => setOnBattery(!b.charging));
      })
      .catch(() => setOnBattery(false));
  }, []);
  const on = choice === "on" || (choice === "auto" && (onBattery || heavy));
  useEffect(() => {
    applyEfficient(on);
    // The efficient mode redraws every five seconds instead of with every
    // sample.
    setLiveThrottle(on ? 5000 : 0);
  }, [on]);
  return on;
}

function Shell(props: {
  session: Session;
  status: Status;
  onSignOut: () => void;
  onLang: (l: string) => void;
}) {
  const route = useRoute();
  const [theme, setTheme] = useState<ThemeChoice>(prefs.theme());
  const [efficient, setEfficient] = useState<EfficientChoice>(prefs.efficient());
  const effective = useEfficient(efficient, props.status);
  useEffect(() => {
    applyTheme(theme);
    onSystemTheme(() => applyTheme(prefs.theme()));
  }, [theme]);
  // A node without a calibration opens the first steps once, until the
  // user finishes or skips them.
  useEffect(() => {
    if (!props.status.calibration && !prefs.welcomed() && route.path.length === 0) {
      location.hash = href("welcome");
    }
  }, []);
  const id = route.path[0] ?? "summary";
  const visible = screens.filter((s) => !s.admin || props.session.role === "admin");
  const screen = visible.find((s) => s.id === id) ?? visible[0];
  useEffect(() => {
    document.title = `${t(`gui.nav.${screen?.id ?? "summary"}`)} - GreenLens`;
  }, [screen]);
  return (
    <div class="shell">
      {/* The hash holds the route, so skipping to the content moves the
          focus instead of changing the address. */}
      <button type="button" class="skip" onClick={() => document.getElementById("main")?.focus()}>
        {t("gui.skip")}
      </button>
      <nav class="glass glass-2 sidebar" aria-label={t("gui.nav.label")}>
        <a class="brand" href={href()}>
          {icons.logo()} GreenLens
        </a>
        <ul class="nav">
          {visible
            .filter((s) => !s.hidden)
            .map((s) => (
              <li key={s.id}>
                <a href={href(s.id)} aria-current={s.id === screen?.id ? "page" : undefined}>
                  {s.icon()}
                  {t(`gui.nav.${s.id}`)}
                </a>
              </li>
            ))}
        </ul>
        <div class="sidebar-foot">
          <details class="prefs">
            <summary>{t("gui.prefs.title")}</summary>
            <div class="prefs-body">
              <label for="lang">{t("gui.prefs.language")}</label>
              <select
                id="lang"
                class="input"
                value={lang()}
                onChange={(e) => props.onLang((e.target as HTMLSelectElement).value)}
              >
                {languages().map((l) => (
                  <option key={l} value={l}>
                    {t(`gui.language.${l}`)}
                  </option>
                ))}
              </select>
              <label for="theme">{t("gui.prefs.theme")}</label>
              <select
                id="theme"
                class="input"
                value={theme}
                onChange={(e) => {
                  const v = (e.target as HTMLSelectElement).value as ThemeChoice;
                  prefs.setTheme(v);
                  setTheme(v);
                }}
              >
                {(["system", "dark", "light"] as const).map((v) => (
                  <option key={v} value={v}>
                    {t(`gui.theme.${v}`)}
                  </option>
                ))}
              </select>
              <label for="efficient">{t("gui.prefs.efficient")}</label>
              <select
                id="efficient"
                class="input"
                value={efficient}
                aria-describedby="efficient-state"
                onChange={(e) => {
                  const v = (e.target as HTMLSelectElement).value as EfficientChoice;
                  prefs.setEfficient(v);
                  setEfficient(v);
                }}
              >
                {(["auto", "on", "off"] as const).map((v) => (
                  <option key={v} value={v}>
                    {t(`gui.efficient.${v}`)}
                  </option>
                ))}
              </select>
              <span id="efficient-state">
                {effective ? t("gui.efficient.active") : t("gui.efficient.inactive")}
              </span>
            </div>
          </details>
          <span>
            {t("gui.session.as", { actor: props.session.actor, role: t(`gui.role.${props.session.role}`) })}
          </span>
          {props.session.via === "session" ? (
            <button type="button" class="btn" onClick={props.onSignOut}>
              {icons.signOut()}
              {t("gui.session.sign_out")}
            </button>
          ) : null}
        </div>
      </nav>
      <main id="main" class="main" tabIndex={-1}>
        {screen?.render({
          status: props.status,
          session: props.session,
          path: route.path.slice(1),
          query: route.query,
        })}
      </main>
    </div>
  );
}

type Phase =
  | { kind: "loading" }
  | { kind: "error"; error: unknown }
  | { kind: "login" }
  | { kind: "ready"; session: Session; status: Status };

export function App() {
  const route = useRoute();
  const [phase, setPhase] = useState<Phase>({ kind: "loading" });
  const [, setLangTick] = useState(0);

  const start = useCallback(async () => {
    try {
      const session = await api.session();
      setCsrf(session.csrf);
      setPhase({ kind: "ready", session, status: await api.status() });
    } catch (e) {
      if (e instanceof ApiError && e.status === 401) {
        setPhase({ kind: "login" });
      } else {
        setPhase({ kind: "error", error: e });
      }
    }
  }, []);

  useEffect(() => {
    applyTheme(prefs.theme());
    loadCatalog(pickLanguage(prefs.lang()))
      .then(start)
      .catch((e: unknown) => setPhase({ kind: "error", error: e }));
  }, [start]);

  // The state of the node changes slowly; it is read again every minute.
  useEffect(() => {
    if (phase.kind !== "ready") return;
    const id = window.setInterval(() => {
      api
        .status()
        .then((status) => setPhase((p) => (p.kind === "ready" ? { ...p, status } : p)))
        .catch(() => undefined);
    }, 60000);
    return () => window.clearInterval(id);
  }, [phase.kind]);

  const changeLang = (l: string) => {
    prefs.setLang(l);
    loadCatalog(l)
      .then(() => setLangTick((n) => n + 1))
      .catch((e: unknown) => setPhase({ kind: "error", error: e }));
  };
  const signOut = () => {
    api
      .signOut()
      .then(() => {
        setCsrf(undefined);
        setPhase({ kind: "login" });
      })
      .catch((e: unknown) => setPhase({ kind: "error", error: e }));
  };

  return (
    <>
      <div class="backdrop" aria-hidden="true" />
      {phase.kind === "loading" ? (
        <main id="main" class="main">
          <Loading />
        </main>
      ) : phase.kind === "error" ? (
        <main id="main" class="main">
          <ErrorNotice error={phase.error} />
        </main>
      ) : phase.kind === "login" ? (
        <Login expired={route.query.get("expired") === "1"} onSignedIn={() => start()} />
      ) : (
        <Shell session={phase.session} status={phase.status} onSignOut={signOut} onLang={changeLang} />
      )}
    </>
  );
}
