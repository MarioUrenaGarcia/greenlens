// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

import type { ComponentChildren } from "preact";
import { useState } from "preact/hooks";
import { t } from "../i18n";
import { icons } from "./icons";

export interface Column<R> {
  key: string;
  label: string;
  numeric?: boolean;
  // sort returns the value rows are ordered by; without it the column
  // cannot be sorted.
  sort?: (r: R) => number | string;
  cell: (r: R) => ComponentChildren;
}

// DataTable is a table whose columns sort with their header buttons, which
// announce the order to assistive technology.
export function DataTable<R>(props: {
  caption: string;
  columns: Column<R>[];
  rows: R[];
  rowKey: (r: R) => string;
  initial?: { key: string; desc: boolean };
}) {
  const [order, setOrder] = useState(props.initial ?? null);
  const col = order ? props.columns.find((c) => c.key === order.key) : undefined;
  const rows = [...props.rows];
  if (order && col?.sort) {
    const f = col.sort;
    rows.sort((a, b) => {
      const x = f(a);
      const y = f(b);
      const c = typeof x === "number" && typeof y === "number" ? x - y : String(x).localeCompare(String(y));
      return order.desc ? -c : c;
    });
  }
  return (
    <div class="table-wrap">
      <table>
        <caption>{props.caption}</caption>
        <thead>
          <tr>
            {props.columns.map((c) => {
              const active = order?.key === c.key;
              const aria = active ? (order?.desc ? "descending" : "ascending") : "none";
              return (
                <th
                  key={c.key}
                  scope="col"
                  class={c.numeric ? "n" : ""}
                  aria-sort={c.sort ? aria : undefined}
                >
                  {c.sort ? (
                    <button
                      type="button"
                      onClick={() => setOrder({ key: c.key, desc: active ? !order?.desc : true })}
                      title={t("gui.table.sort", { column: c.label })}
                    >
                      {c.label}
                      {active ? (order?.desc ? icons.down() : icons.up()) : icons.sort()}
                    </button>
                  ) : (
                    c.label
                  )}
                </th>
              );
            })}
          </tr>
        </thead>
        <tbody>
          {rows.map((r) => (
            <tr key={props.rowKey(r)}>
              {props.columns.map((c) => (
                <td key={c.key} class={c.numeric ? "n num" : ""}>
                  {c.cell(r)}
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
