// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

// save offers the browser a file built in the page, so exports need no
// round trip to the agent.
export function save(name: string, type: string, content: string): void {
  const url = URL.createObjectURL(new Blob([content], { type }));
  const a = document.createElement("a");
  a.href = url;
  a.download = name;
  document.body.append(a);
  a.click();
  a.remove();
  window.setTimeout(() => URL.revokeObjectURL(url), 1000);
}

// csv writes rows with the quoting of RFC 4180.
export function csv(rows: (string | number | boolean)[][]): string {
  const cell = (v: string | number | boolean) => {
    const s = String(v);
    return /[",\n\r]/.test(s) ? `"${s.replace(/"/g, '""')}"` : s;
  };
  return `${rows.map((r) => r.map(cell).join(",")).join("\r\n")}\r\n`;
}

export function stamp(): string {
  return new Date().toISOString().replace(/[:T]/g, "-").slice(0, 19);
}
