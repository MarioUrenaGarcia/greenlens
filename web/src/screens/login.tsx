// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

import { useState } from "preact/hooks";
import { ApiError, api } from "../api";
import { icons } from "../components/icons";
import { t } from "../i18n";
import type { Session } from "../types";

export function Login(props: { expired: boolean; onSignedIn: (s: Session) => void }) {
  const [token, setToken] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const submit = async (e: Event) => {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      props.onSignedIn(await api.signIn(token.trim()));
    } catch (err) {
      setError(err instanceof ApiError && err.status === 401 ? t("gui.login.invalid") : String(err));
    } finally {
      setBusy(false);
    }
  };
  return (
    <main id="main" class="glass glass-2 login">
      <h1 class="brand">{icons.logo()} GreenLens</h1>
      {props.expired ? (
        <p class="notice" role="status">
          {t("gui.login.expired")}
        </p>
      ) : null}
      <p>{t("gui.login.intro")}</p>
      <pre class="inner" style={{ padding: "var(--space-3)" }}>
        <code>greenlens gui</code>
      </pre>
      <p>{t("gui.login.token_intro")}</p>
      <form onSubmit={submit}>
        <div class="field">
          <label for="token">{t("gui.login.token")}</label>
          <input
            id="token"
            class="input"
            type="password"
            autocomplete="off"
            spellcheck={false}
            required
            value={token}
            onInput={(e) => setToken((e.target as HTMLInputElement).value)}
            aria-describedby={error ? "login-error" : undefined}
          />
        </div>
        {error ? (
          <p id="login-error" class="notice notice-error" role="alert">
            {error}
          </p>
        ) : null}
        <button type="submit" class="btn btn-primary" disabled={busy || token.trim() === ""}>
          {t("gui.login.submit")}
        </button>
      </form>
    </main>
  );
}
