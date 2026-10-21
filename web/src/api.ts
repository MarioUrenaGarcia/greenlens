// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

import type { BackendUsage, Backends, ErrorBody, Session, Status, Usage } from "./types";

// ApiError carries the status and the message of a refused request.
export class ApiError extends Error {
  readonly status: number;
  constructor(status: number, message: string) {
    super(message);
    this.status = status;
  }
}

let csrf = "";

// setCsrf keeps the token that requests of the session must send when they
// change something.
export function setCsrf(token: string | undefined): void {
  csrf = token ?? "";
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const headers: Record<string, string> = { Accept: "application/json" };
  if (body !== undefined) {
    headers["Content-Type"] = "application/json";
  }
  if (method !== "GET" && csrf) {
    headers["X-GreenLens-CSRF"] = csrf;
  }
  const init: RequestInit = { method, headers, credentials: "same-origin" };
  if (body !== undefined) {
    init.body = JSON.stringify(body);
  }
  const resp = await fetch(path, init);
  if (!resp.ok) {
    let msg = resp.statusText;
    try {
      msg = ((await resp.json()) as ErrorBody).error || msg;
    } catch {
      // Not every error carries a JSON body; the status text stays.
    }
    throw new ApiError(resp.status, msg);
  }
  if (resp.status === 204) {
    return undefined as T;
  }
  return (await resp.json()) as T;
}

function iso(d: Date): string {
  return d.toISOString().replace(/\.\d{3}Z$/, "Z");
}

export const api = {
  session: () => request<Session>("GET", "/api/v1/session"),
  signIn: (token: string) => request<Session>("POST", "/api/v1/session", { token }),
  signOut: () => request<undefined>("DELETE", "/api/v1/session"),
  status: () => request<Status>("GET", "/api/v1/status"),
  backends: () => request<Backends>("GET", "/api/v1/backends"),
  usage: (from: Date, to: Date, bucket?: string) => {
    const q = new URLSearchParams({ from: iso(from), to: iso(to) });
    if (bucket) q.set("bucket", bucket);
    return request<Usage>("GET", `/api/v1/usage?${q}`);
  },
  usageBy: (from: Date, to: Date, groupBy: "backend" | "model" | "consumer") => {
    const q = new URLSearchParams({ from: iso(from), to: iso(to), group_by: groupBy });
    return request<BackendUsage>("GET", `/api/v1/usage?${q}`);
  },
  request,
};
