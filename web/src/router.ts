// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

import { useEffect, useState } from "preact/hooks";

export interface Route {
  path: string[];
  query: URLSearchParams;
}

function parse(): Route {
  const hash = location.hash.replace(/^#\/?/, "");
  const [p = "", q = ""] = hash.split("?", 2);
  return { path: p.split("/").filter(Boolean).map(decodeURIComponent), query: new URLSearchParams(q) };
}

// useRoute follows the part of the address after "#", so the interface
// works from the files served by the agent without server routes.
export function useRoute(): Route {
  const [route, setRoute] = useState(parse);
  useEffect(() => {
    const f = () => setRoute(parse());
    window.addEventListener("hashchange", f);
    return () => window.removeEventListener("hashchange", f);
  }, []);
  return route;
}

export function href(...parts: string[]): string {
  return `#/${parts.map(encodeURIComponent).join("/")}`;
}
