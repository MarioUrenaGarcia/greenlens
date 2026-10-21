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
import { Login } from "./screens/login";
import { Summary } from "./screens/summary";
import type { Session, Status } from "./types";

interface Screen {
  id: string;
  icon: () => ComponentChildren;
  render: (ctx: { status: Status; session: Session; path: string[] }) => ComponentChildren;
}

const screens: Screen[] = [
  { id: "summary", icon: icons.summary, render: ({ status }) => <Summary status={status} /> },
];

interface Battery {
  charging: boolean;
  addEventListener: (type: "chargingchange", f: () => void) => void;
}

// useEfficient decides whether the efficient mode is on: by the choice of
// the user, or, in automatic mode, while this device runs on battery.
function useEfficient(choice: EfficientChoice): boolean {
  const [onBattery, setOnBattery] = useState(false);
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
  const on = choice === "on" || (choice === "auto" && onBattery);
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
  const effective = useEfficient(efficient);
  useEffect(() => {
    applyTheme(theme);
    onSystemTheme(() => applyTheme(prefs.theme()));
  }, [theme]);
  const id = route.path[0] ?? "summary";
  const screen = screens.find((s) => s.id === id) ?? screens[0];
  useEffect(() => {
    document.title = `${t(`gui.nav.${screen?.id ?? "summary"}`)} - GreenLens`;
  }, [screen]);
  return (
    <div class="shell">
      <a class="skip" href="#main">
        {t("gui.skip")}
      </a>
      <nav class="glass glass-2 sidebar" aria-label={t("gui.nav.label")}>
        <a class="brand" href={href()}>
          {icons.logo()} GreenLens
        </a>
        <ul class="nav">
          {screens.map((s) => (
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
        {screen?.render({ status: props.status, session: props.session, path: route.path.slice(1) })}
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
