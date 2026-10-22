// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

// The public demo of the interface. It answers the API of the agent inside
// the browser from a simulated workspace, so the real interface runs on a
// static page. Every value is invented by the generator of the site;
// nothing comes from a real machine. It is loaded before the interface and
// is never part of the agent.

import type {
  Alert,
  AlertRule,
  Alerts,
  Backend,
  Benchmark,
  BenchmarkJob,
  BenchmarkRequest,
  ChannelEnergy,
  ContextEvent,
  Factors,
  Finding,
  Indicators,
  SCIRow,
  Status,
  UsageEnergy,
} from "../types";

interface ModelData {
  backend: string;
  kind: string;
  endpoint: string;
  model: string;
  energy_j: number[];
  gpu_j: number[];
  generated: number[];
  prompt: number[];
  requests: number[];
}

interface Workspace {
  end: string;
  hours: number;
  ahead_hours: number;
  domains_w: Record<string, number[]>;
  models: ModelData[];
  consumers: { name: string; share: number }[];
  factors: Factors;
  embodied: { server_kgco2e?: number; gpu_kgco2e?: number; lifetime_years?: number };
  idle_w: Record<string, number>;
  benchmarks: Benchmark[];
  events: ContextEvent[];
  regression: {
    baseline_days: number;
    min_baseline_days: number;
    z_threshold: number;
    min_generated_tokens: number;
  };
}

const hourMs = 3.6e6;
const weekMs = 7 * 24 * hourMs;
const here = new URL(".", (document.currentScript as HTMLScriptElement | null)?.src ?? location.href);
const realFetch = window.fetch.bind(window);

// The site links to the demo in the language of its page.
const asked = new URLSearchParams(location.search).get("lang");
if (asked === "en" || asked === "es") {
  try {
    localStorage.setItem("greenlens.lang", asked);
  } catch {
    // Without storage the interface follows the languages of the browser.
  }
}
const channels: Record<string, { id: string; source: string }> = {
  package: { id: "rapl:package-0", source: "rapl" },
  dram: { id: "rapl:dram-0", source: "rapl" },
  gpu: { id: "nvml:0000:01:00.0", source: "nvml" },
};
const processes: Record<string, { pid: number; name: string }> = {
  ollama: { pid: 2141, name: "ollama" },
  vllm: { pid: 3318, name: "python3" },
  llamacpp: { pid: 4072, name: "llama-server" },
};

let ws: Workspace;
const ready: Promise<void> = realFetch(new URL("data/workspace.json", here))
  .then((r) => {
    if (!r.ok) throw new Error(`data/workspace.json: ${r.status}`);
    return r.json() as Promise<Workspace>;
  })
  .then((w) => {
    ws = w;
    const s = shiftMs();
    for (const b of ws.benchmarks) {
      b.started_at = iso(Date.parse(b.started_at) + s);
      b.ended_at = iso(Date.parse(b.ended_at) + s);
    }
  });

// The workspace starts ws.hours before the hour it was generated and runs
// a week past it. The demo moves it by whole weeks, so every hour keeps its
// weekday and time of day in the time zone of the agent, and the current
// hour always falls inside it.
function shiftMs(): number {
  const late = Math.floor(Date.now() / hourMs) * hourMs - Date.parse(ws.end);
  return Math.max(0, Math.floor(late / weekMs)) * weekMs;
}

function startMs(): number {
  return Date.parse(ws.end) + shiftMs() - ws.hours * hourMs;
}

function at(series: number[], i: number): number {
  return i < 0 ? 0 : (series[Math.min(i, series.length - 1)] ?? 0);
}

// sum adds up an hourly series over [from, to): per-hour totals in part,
// or watts times seconds when watts is set.
function sum(series: number[], from: number, to: number, watts = false): number {
  const s0 = startMs();
  let total = 0;
  for (let i = Math.floor((from - s0) / hourMs); s0 + i * hourMs < to; i++) {
    const lo = Math.max(from, s0 + i * hourMs);
    const hi = Math.min(to, s0 + (i + 1) * hourMs);
    if (hi <= lo) continue;
    const part = (hi - lo) / hourMs;
    total += watts ? at(series, i) * part * 3600 : at(series, i) * part;
  }
  return total;
}

function indicators(j: number): Indicators {
  const kwh = j / 3.6e6;
  return { kwh, co2e_g: kwh * ws.factors.emission_g_per_kwh * ws.factors.pue };
}

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

function refused(): Response {
  return json({ error: catalogText("gui.demo.readonly") }, 403);
}

let catalog: Record<string, string> = {};
function catalogText(key: string): string {
  return catalog[key] ?? key;
}

function lang(url: URL): string {
  const l = url.searchParams.get("lang") ?? "en";
  return l === "es" ? "es" : "en";
}

function buckets(from: number, to: number, bucket: number): [number, number][] {
  if (!(bucket > 0)) return [[from, to]];
  const out: [number, number][] = [];
  for (let b = from; b < to; b += bucket) out.push([b, Math.min(b + bucket, to)]);
  return out;
}

function duration(s: string | null): number {
  if (!s) return 0;
  const m = /^(\d+)([mh])$/.exec(s);
  if (!m) return 0;
  return Number(m[1]) * (m[2] === "h" ? hourMs : 6e4);
}

function nodeUsage(from: number, to: number, bucket: number) {
  const rows: ChannelEnergy[] = [];
  for (const [lo, hi] of buckets(from, to, bucket)) {
    for (const [domain, series] of Object.entries(ws.domains_w)) {
      const ch = channels[domain];
      if (!ch) continue;
      const j = sum(series, lo, hi, true);
      rows.push({
        bucket_start: new Date(lo).toISOString(),
        channel: ch.id,
        source: ch.source,
        domain,
        quality: "measured",
        energy_j: j,
        samples: Math.round((hi - lo) / 1000),
        indicators: indicators(j),
      });
    }
  }
  return { schema_version: 1, from: iso(from), to: iso(to), bucket_ms: bucket, resolution: "1m", rows };
}

function iso(ms: number): string {
  return new Date(ms).toISOString();
}

interface Use {
  energy: number;
  gpu: number;
  generated: number;
  prompt: number;
  requests: number;
}

function use(m: ModelData, from: number, to: number): Use {
  return {
    energy: sum(m.energy_j, from, to),
    gpu: sum(m.gpu_j, from, to),
    generated: Math.round(sum(m.generated, from, to)),
    prompt: Math.round(sum(m.prompt, from, to)),
    requests: Math.round(sum(m.requests, from, to)),
  };
}

function groupUsage(from: number, to: number, by: string, bucket: number) {
  const rows: (UsageEnergy & { gpu_energy_j: number })[] = [];
  for (const [lo, hi] of buckets(from, to, bucket)) {
    const part: (UsageEnergy & { gpu_energy_j: number })[] = [];
    const add = (backend: string, kind: string, model: string, consumer: string, u: Use, share: number) => {
      let r = part.find(
        (x) => x.backend === backend && (x.model ?? "") === model && (x.consumer ?? "") === consumer,
      );
      if (!r) {
        r = {
          backend,
          kind,
          energy_j: 0,
          gpu_energy_j: 0,
          prompt_tokens: 0,
          generated_tokens: 0,
          requests: 0,
          tokens_counted: true,
        };
        if (bucket > 0) r.bucket_start = iso(lo);
        if (model) r.model = model;
        if (consumer) r.consumer = consumer;
        part.push(r);
      }
      r.energy_j += u.energy * share;
      r.gpu_energy_j += u.gpu * share;
      r.generated_tokens += Math.round(u.generated * share);
      r.prompt_tokens += Math.round(u.prompt * share);
      r.requests += Math.round(u.requests * share);
    };
    for (const m of ws.models) {
      const u = use(m, lo, hi);
      if (by === "model") add(m.backend, m.kind, m.model, "", u, 1);
      else if (by === "consumer")
        for (const c of ws.consumers) add(m.backend, m.kind, "", c.name, u, c.share);
      else add(m.backend, m.kind, "", "", u, 1);
    }
    part.sort((a, b) => b.energy_j - a.energy_j);
    rows.push(...part);
  }
  for (const r of rows) r.indicators = indicators(r.energy_j);
  return {
    schema_version: 1,
    from: iso(from),
    to: iso(to),
    group_by: by,
    bucket_ms: bucket,
    resolution: "1m",
    rows,
  };
}

const now = () => Date.now();
const startedAt = () => now() - 6 * 24 * hourMs - 25 * 6e4;

function backends(): Backend[] {
  const since = startedAt();
  const t = now();
  const out: Backend[] = [];
  for (const m of ws.models) {
    let b = out.find((x) => x.name === m.backend);
    if (!b) {
      const p = processes[m.kind];
      b = {
        name: m.backend,
        kind: m.kind,
        endpoint: m.endpoint,
        static: false,
        processes: m.kind === "vllm" ? 3 : 1,
        // The proxy of GreenLens counts the tokens of every backend here.
        token_counters: true,
        last_read: iso(t),
        power_w: 0,
        energy_j: 0,
        cpu_energy_j: 0,
        dram_energy_j: 0,
        gpu_energy_j: 0,
        indicators: indicators(0),
        models: [],
      };
      if (p) b.roots = [p];
      out.push(b);
    }
    const u = use(m, since, t);
    const cpu = u.energy - u.gpu;
    const live = sum(m.energy_j, t - 6e4, t) / 60;
    b.power_w += live;
    b.energy_j += u.energy;
    b.gpu_energy_j += u.gpu;
    b.cpu_energy_j += cpu * 0.85;
    b.dram_energy_j += cpu * 0.15;
    b.models?.push({
      name: m.model,
      energy_j: u.energy,
      prompt_tokens: u.prompt,
      generated_tokens: u.generated,
      requests: u.requests,
      joules_per_generated_token: u.generated > 0 ? u.energy / u.generated : 0,
      method: "token_weighted",
      quality: "measured",
      indicators: indicators(u.energy),
    });
  }
  for (const b of out) {
    b.indicators = indicators(b.energy_j);
    b.models?.sort((x, y) => y.energy_j - x.energy_j);
  }
  return out;
}

// hash01 is a number in [0, 1) that depends only on n, so every visitor
// sees the same load at the same second.
function hash01(n: number): number {
  let x = Math.imul(n | 0, 0x9e3779b1);
  x ^= x >>> 15;
  x = Math.imul(x, 0x85ebca6b);
  x ^= x >>> 13;
  x = Math.imul(x, 0xc2b2ae35);
  x ^= x >>> 16;
  return (x >>> 0) / 4294967296;
}

// powerAt is the power of a domain at one moment: the idle power plus the
// load of its hour, which comes and goes in bursts of a few seconds as
// requests arrive and averages to the mean of the hour.
function powerAt(domain: string, ms: number): number {
  const mean = at(ws.domains_w[domain] ?? [], Math.floor((ms - startMs()) / hourMs));
  const idle = Math.min(ws.idle_w[domain] ?? 0, mean);
  const s = ms / 1000;
  const k = Math.floor(s / 5);
  const x = s / 5 - k;
  const f = x * x * (3 - 2 * x);
  const burst = hash01(k) * (1 - f) + hash01(k + 1) * f;
  const noise = (hash01(Math.floor(s) * 7 + domain.length) - 0.5) * 0.02;
  return (idle + (mean - idle) * (0.3 + 1.4 * burst)) * (1 + noise);
}

function status(): Status {
  const t = now();
  const since = startedAt();
  return {
    schema_version: 1,
    version: "demo",
    started_at: iso(since),
    interval_ms: 1000,
    sample_time: iso(t),
    samples: Math.round((t - since) / 1000),
    estimated: false,
    domains: Object.keys(ws.domains_w).map((d) => ({
      domain: d,
      level: "component",
      power_w: powerAt(d, t),
      energy_j: sum(ws.domains_w[d] ?? [], since, t, true),
      quality: "measured",
    })),
    channels: [
      ...Object.entries(channels).map(([domain, c]) => ({
        id: c.id,
        source: c.source,
        domain,
        quality: "measured",
        method: c.source === "nvml" ? "counter" : "counter",
        selected: true,
        reason: "selector.reason.selected",
      })),
      {
        id: "estimate:cpu",
        source: "estimate",
        domain: "package",
        quality: "estimated",
        method: "estimated",
        selected: false,
        reason: "selector.reason.duplicate",
        reason_args: { source: "rapl" },
      },
    ],
    problems: [],
    calibration: {
      at: iso(since + hourMs),
      baseline: {
        cpu_w: ws.idle_w.package ?? 0,
        dram_w: ws.idle_w.dram ?? 0,
        platform_w: 0,
        gpu_w: { "nvml:0000:01:00.0": ws.idle_w.gpu ?? 0 },
      },
    },
    self: { energy_j: 1840, cpu_seconds: 412, rss_bytes: 31_000_000 },
    idle_policy: "dynamic",
    factors: ws.factors,
    cpu_joules_per_busy_second: 9.4,
    backends: backends(),
  };
}

// sci computes the carbon intensity of the node and of each model as the
// agent does: operational emissions plus the embodied carbon of the server
// and the GPU in the share of their energy, per thousand tokens.
function sci(from: number, to: number, groupBy: string) {
  const serverJ =
    sum(ws.domains_w.package ?? [], from, to, true) + sum(ws.domains_w.dram ?? [], from, to, true);
  const gpuJ = sum(ws.domains_w.gpu ?? [], from, to, true);
  const e = ws.embodied;
  const years = e.lifetime_years ?? 0;
  const used = years > 0 ? (to - from) / hourMs / (8766 * years) : 0;
  const embodied = (ss: number, gs: number) =>
    used > 0 ? ((e.server_kgco2e ?? 0) * ss + (e.gpu_kgco2e ?? 0) * gs) * 1000 * used : undefined;
  const row = (
    energy: number,
    gpu: number,
    tokens: number,
    requests: number,
    ss: number,
    gs: number,
  ): SCIRow => {
    const ind = indicators(energy);
    const m = embodied(ss, gs);
    const units = tokens / 1000;
    const s: SCIRow["sci"] = { functional_unit: "1k_generated_tokens", units, operational_g: ind.co2e_g };
    if (m !== undefined) s.embodied_g = m;
    if (units > 0) s.g_per_unit = (ind.co2e_g + (m ?? 0)) / units;
    return {
      energy_j: energy,
      server_energy_j: energy - gpu,
      gpu_energy_j: gpu,
      server_share: ss,
      gpu_share: gs,
      generated_tokens: tokens,
      requests,
      consumers: 0,
      indicators: ind,
      sci: s,
    };
  };
  let tokens = 0;
  let requests = 0;
  const rows: SCIRow[] = [];
  for (const m of ws.models) {
    const u = use(m, from, to);
    tokens += u.generated;
    requests += u.requests;
    if (groupBy === "model") {
      const r = row(u.energy, u.gpu, u.generated, u.requests, (u.energy - u.gpu) / serverJ, u.gpu / gpuJ);
      rows.push({ ...r, backend: m.backend, kind: m.kind, model: m.model });
    }
  }
  return {
    schema_version: 1,
    from: iso(from),
    to: iso(to),
    group_by: groupBy || undefined,
    functional_unit: "1k_generated_tokens",
    factors: ws.factors,
    embodied: ws.embodied,
    missing: [],
    node: row(serverJ + gpuJ, gpuJ, tokens, requests, 1, 1),
    rows,
  };
}

const zone = () => ws.factors.time_zone;

// zoneOffsetMs is how far the time zone of the agent is ahead of UTC at ms.
function zoneOffsetMs(ms: number): number {
  const parts = new Intl.DateTimeFormat("en-US", {
    timeZone: zone(),
    hourCycle: "h23",
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  }).formatToParts(new Date(ms));
  const v = (type: string) => Number(parts.find((x) => x.type === type)?.value);
  return (
    Date.UTC(v("year"), v("month") - 1, v("day"), v("hour"), v("minute"), v("second")) -
    Math.floor(ms / 1000) * 1000
  );
}

function localDay(ms: number): string {
  return new Intl.DateTimeFormat("en-CA", {
    timeZone: zone(),
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
  }).format(new Date(ms));
}

// regressions judges each local day of a model against the days before
// it, as the agent does.
function regressions(from: number, to: number, backend: string | null, model: string | null) {
  const p = ws.regression;
  const firstDay = localDay(from);
  const models = [];
  for (const m of ws.models) {
    if ((backend && backend !== m.backend) || (model && model !== m.model)) continue;
    const days = new Map<string, { e: number; g: number }>();
    for (let t = from - (p.baseline_days + 1) * 24 * hourMs; t < to; t += hourMs) {
      const d = localDay(t);
      const cur = days.get(d) ?? { e: 0, g: 0 };
      cur.e += sum(m.energy_j, t, Math.min(t + hourMs, to));
      cur.g += sum(m.generated, t, Math.min(t + hourMs, to));
      days.set(d, cur);
    }
    const list = [...days.entries()].filter(([, v]) => v.g >= p.min_generated_tokens);
    const found: Finding[] = [];
    list.forEach(([day, v], i) => {
      if (day < firstDay) return;
      const before = new Date(`${day}T00:00:00Z`).getTime() - p.baseline_days * 24 * hourMs;
      const base = list
        .slice(0, i)
        .filter(([d]) => new Date(`${d}T00:00:00Z`).getTime() >= before)
        .map(([, b]) => b.e / b.g);
      const x = v.e / v.g;
      const f: Finding = {
        day,
        energy_j: v.e,
        generated_tokens: Math.round(v.g),
        j_per_generated_token: x,
        baseline_days: base.length,
        baseline_mean: 0,
        baseline_sd: 0,
        status: "insufficient",
      };
      if (base.length >= Math.max(p.min_baseline_days, 2)) {
        const mean = base.reduce((a, b) => a + b, 0) / base.length;
        const sd = Math.sqrt(base.reduce((a, b) => a + (b - mean) ** 2, 0) / (base.length - 1));
        const z = (x - mean) / Math.max(sd, 0.005 * mean);
        f.baseline_mean = mean;
        f.baseline_sd = sd;
        f.z = z;
        f.status = z >= p.z_threshold ? "regression" : z <= -p.z_threshold ? "improvement" : "normal";
      }
      found.push(f);
    });
    models.push({ backend: m.backend, model: m.model, days: found });
  }
  const shift = shiftMs();
  const events = ws.events
    .map((e) => ({ ...e, at: iso(Date.parse(e.at) + shift) }))
    .filter((e) => Date.parse(e.at) >= from - 24 * hourMs && Date.parse(e.at) < to);
  return { schema_version: 1, from: iso(from), to: iso(to), time_zone: zone(), params: p, models, events };
}

// The alerts of the demo come from its own data: vLLM above 40 W through
// the busy hours, and the days the regression of llama3.2:3b stands out.
const demoRules: AlertRule[] = [
  {
    name: "vllm_busy",
    expr: "backend_power_watts",
    op: ">",
    threshold: 40,
    for: "5m",
    match: { backend: "vllm-8000" },
  },
  { name: "efficiency_regression", expr: "model_jpt_zscore", op: ">", threshold: 3, for: "30m" },
  { name: "energy_source_down", expr: "source_up", op: "<", threshold: 1, for: "1m" },
];

function alertsDoc(fromQ: number, toQ: number): Alerts {
  const t = now();
  const to = Number.isNaN(toQ) ? t : Math.min(toQ, t);
  const from = Number.isNaN(fromQ) ? to - 7 * 24 * hourMs : fromQ;
  const history: Alert[] = [];
  const active: Alert[] = [];
  let id = 1;
  const vllm = ws.models.filter((m) => m.backend === "vllm-8000");
  const powerAtHour = (h: number) => vllm.reduce((a, m) => a + sum(m.energy_j, h, h + hourMs), 0) / 3600;
  const busy = demoRules[0] as AlertRule;
  let run: { start: number; value: number } | null = null;
  const first = Math.floor(from / hourMs) * hourMs;
  for (let h = first; h <= to; h += hourMs) {
    const w =
      h + hourMs > t ? (backends().find((b) => b.name === "vllm-8000")?.power_w ?? 0) : powerAtHour(h);
    if (w > busy.threshold) {
      run = run ?? { start: h, value: w };
      run.value = w;
      continue;
    }
    if (run) {
      history.push({
        id: id++,
        rule: busy.name,
        expr: busy.expr,
        op: busy.op,
        threshold: busy.threshold,
        for_s: 300,
        labels: { backend: "vllm-8000" },
        state: "resolved",
        value: w,
        started_at: iso(run.start),
        fired_at: iso(run.start + 5 * 6e4),
        resolved_at: iso(h),
      });
      run = null;
    }
  }
  if (run) {
    active.push({
      id: id++,
      rule: busy.name,
      expr: busy.expr,
      op: busy.op,
      threshold: busy.threshold,
      for_s: 300,
      labels: { backend: "vllm-8000" },
      state: "firing",
      value: run.value,
      started_at: iso(run.start),
      fired_at: iso(run.start + 5 * 6e4),
    });
  }
  const reg = demoRules[1] as AlertRule;
  for (const m of regressions(from, to, "ollama-11434", "llama3.2:3b").models) {
    for (const d of m.days) {
      if (d.status !== "regression" || d.z === undefined) continue;
      const start =
        Date.parse(`${d.day}T00:00:00Z`) - zoneOffsetMs(Date.parse(`${d.day}T12:00:00Z`)) + 2 * hourMs;
      history.push({
        id: id++,
        rule: reg.name,
        expr: reg.expr,
        op: reg.op,
        threshold: reg.threshold,
        for_s: 1800,
        labels: { backend: m.backend, model: m.model },
        state: "resolved",
        value: d.z,
        started_at: iso(start),
        fired_at: iso(start + 30 * 6e4),
        resolved_at: iso(start + 22 * hourMs),
      });
    }
  }
  history.sort((a, b) => Date.parse(b.started_at) - Date.parse(a.started_at));
  return {
    schema_version: 1,
    evaluated_at: iso(t),
    every_s: 15,
    signals: [
      { name: "node_power_watts", labels: [] },
      { name: "backend_power_watts", labels: ["backend"] },
      { name: "model_joules_per_token", labels: ["backend", "model"] },
      { name: "model_jpt_zscore", labels: ["backend", "model"] },
      { name: "source_up", labels: ["source"] },
    ],
    operators: [">", ">=", "<", "<="],
    rules: demoRules,
    sinks: ["log", "webhook"],
    config_writable: false,
    active,
    from: iso(from),
    to: iso(to),
    history: [...active, ...history].filter((a) => a.fired_at),
  };
}

// A benchmark run from the interface plays out in a few seconds.
let job: BenchmarkJob = { schema_version: 1, state: "idle", run: 0, runs: 0 };
const ran: Benchmark[] = [];

function benchmarks(): Benchmark[] {
  return [...ran, ...ws.benchmarks].sort((a, b) => Date.parse(b.started_at) - Date.parse(a.started_at));
}

function startBenchmark(req: BenchmarkRequest): Response {
  if (job.state === "running") return json({ error: "a benchmark is already running" }, 409);
  const m = ws.models.find((x) => x.model === req.model && x.kind === req.kind);
  const like = ws.benchmarks.find((b) => b.target.model === req.model) ?? ws.benchmarks[0];
  if (!m || !like) return json({ error: `no model ${req.model} in the simulated workspace` }, 400);
  const runs = Math.max(2, Math.min(req.runs, 10));
  const started = now();
  job = { schema_version: 1, state: "running", run: 0, runs, request: req, started_at: iso(started) };
  const tick = window.setInterval(() => {
    if (job.state !== "running") {
      window.clearInterval(tick);
      return;
    }
    job = { ...job, run: job.run + 1 };
    if (job.run >= runs) {
      window.clearInterval(tick);
      const id = Math.max(...benchmarks().map((b) => b.id ?? 0)) + 1;
      ran.push({
        ...like,
        id,
        started_at: iso(started),
        ended_at: iso(now()),
        runs: like.runs.slice(0, runs),
      });
      job = { ...job, state: "done", ended_at: iso(now()), benchmark_id: id };
    }
    emit("benchmark", job);
  }, 1500);
  emit("benchmark", job);
  return json(job, 202);
}

// EventSource stands in for the live stream of the agent. As the agent
// does, it first sends the samples of the last fifteen minutes, or those
// after the last one the page received, then one every second. A sample
// is named by its second, so a page that resumes gets each one once.
const streams = new Set<DemoEventSource>();
const replayS = 15 * 60;

function send(s: DemoEventSource, name: string, data: unknown, id = "") {
  s.dispatchEvent(new MessageEvent(name, { data: JSON.stringify(data), lastEventId: id }));
}

function emit(name: string, data: unknown) {
  for (const s of streams) send(s, name, data);
}

function sample(sec: number) {
  const ms = sec * 1000;
  return {
    time: iso(ms),
    interval_ms: 1000,
    power_w: Object.fromEntries(Object.keys(ws.domains_w).map((d) => [d, powerAt(d, ms)])),
  };
}

// samples sends the samples a stream has not received up to now.
function samples(s: DemoEventSource) {
  const last = Math.floor(now() / 1000);
  for (let sec = Math.max(s.sent + 1, last - replayS + 1); sec <= last; sec++)
    send(s, "sample", sample(sec), String(sec));
  s.sent = last;
}

class DemoEventSource extends EventTarget {
  static readonly CONNECTING = 0;
  static readonly OPEN = 1;
  static readonly CLOSED = 2;
  readonly url: string;
  readonly withCredentials: boolean;
  readyState = 0;
  sent: number;
  onopen: ((e: Event) => void) | null = null;
  onerror: ((e: Event) => void) | null = null;
  onmessage: ((e: MessageEvent) => void) | null = null;
  constructor(url: string | URL, init?: EventSourceInit) {
    super();
    this.url = String(url);
    this.withCredentials = init?.withCredentials ?? false;
    this.sent = Number(new URL(this.url, location.href).searchParams.get("last_event_id") ?? 0) || 0;
    void ready.then(() => {
      if (this.readyState === 2) return;
      this.readyState = 1;
      streams.add(this);
      this.onopen?.(new Event("open"));
      samples(this);
      send(this, "backends", { schema_version: 1, interval_ms: 10_000, backends: backends() });
    });
  }
  close() {
    this.readyState = 2;
    streams.delete(this);
  }
}

window.setInterval(() => {
  for (const s of streams) samples(s);
}, 1000);
window.setInterval(() => {
  if (ws && streams.size > 0)
    emit("backends", { schema_version: 1, interval_ms: 10_000, backends: backends() });
}, 10_000);

let signedIn = true;

async function file(name: string): Promise<Response> {
  const r = await realFetch(new URL(name, here));
  if (!r.ok) return json({ error: "not found" }, 404);
  return new Response(await r.text(), { status: 200, headers: { "Content-Type": "application/json" } });
}

async function handle(method: string, url: URL, body: string | null): Promise<Response> {
  const p = url.pathname.replace(/^.*\/api\/v1/, "/api/v1");
  const q = url.searchParams;
  const from = Date.parse(q.get("from") ?? "");
  const to = Date.parse(q.get("to") ?? "");
  if (p.startsWith("/api/v1/catalogs/")) {
    const r = await file(`data/catalog-${p.endsWith("/es") ? "es" : "en"}.json`);
    const doc = (await r.clone().json()) as { messages: Record<string, string> };
    catalog = doc.messages;
    return r;
  }
  if (p === "/api/v1/session") {
    if (method === "DELETE") {
      signedIn = false;
      return new Response(null, { status: 204 });
    }
    if (method === "POST") signedIn = true;
    return signedIn
      ? json({ schema_version: 1, role: "operator", actor: "demo", via: "session", csrf: "demo" })
      : json({ error: "sign in" }, 401);
  }
  if (method !== "GET") {
    if (p === "/api/v1/benchmarks") return startBenchmark(JSON.parse(body ?? "{}") as BenchmarkRequest);
    if (p === "/api/v1/benchmarks/run" && method === "DELETE") {
      job = { ...job, state: "canceled", ended_at: iso(now()) };
      emit("benchmark", job);
      return json(job, 202);
    }
    return refused();
  }
  switch (p) {
    case "/api/v1/status":
      return json(status());
    case "/api/v1/backends":
      return json({ schema_version: 1, interval_ms: 10_000, backends: backends() });
    case "/api/v1/usage": {
      const bucket = duration(q.get("bucket"));
      const by = q.get("group_by");
      return json(by ? groupUsage(from, to, by, bucket) : nodeUsage(from, to, bucket));
    }
    case "/api/v1/reports":
      return q.get("kind") === "esg"
        ? file("reports/esg.json")
        : json(sci(from, to, q.get("group_by") ?? ""));
    case "/api/v1/regressions":
      return json(regressions(from, to, q.get("backend"), q.get("model")));
    case "/api/v1/alerts":
      return json(alertsDoc(from, to));
    case "/api/v1/benchmarks":
      return json({ schema_version: 1, benchmarks: benchmarks() });
    case "/api/v1/benchmarks/run":
      return json(job);
    case "/api/v1/calibrations/run":
      return json({ schema_version: 1, state: "idle", duration_s: 0 });
    case "/api/v1/about":
      return file("data/about.json");
    case "/api/v1/help": {
      const l = lang(url);
      const words = (q.get("q") ?? "").toLowerCase().split(/\s+/).filter(Boolean);
      const idx = await file(`data/help/${l}/index.json`);
      if (words.length === 0) return idx;
      const doc = (await idx.json()) as { pages: { path: string; title: string; description?: string }[] };
      const results = doc.pages
        .filter((pg) => words.every((w) => `${pg.title} ${pg.description ?? ""}`.toLowerCase().includes(w)))
        .map((pg) => ({ path: pg.path, title: pg.title, snippet: pg.description ?? "" }));
      return json({ schema_version: 1, lang: l, query: q.get("q"), results });
    }
  }
  if (p.startsWith("/api/v1/help/"))
    return file(`data/help/${lang(url)}/${p.slice("/api/v1/help/".length) || "_index"}.json`);
  if (p.startsWith("/api/v1/benchmarks/")) {
    const id = Number(p.split("/").pop());
    const b = benchmarks().find((x) => x.id === id);
    return b ? json(b) : json({ error: "no such benchmark" }, 404);
  }
  return refused();
}

window.fetch = async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
  const url = new URL(
    typeof input === "string" ? input : input instanceof URL ? input.href : input.url,
    location.href,
  );
  if (!url.pathname.includes("/api/v1/")) return realFetch(input, init);
  await ready;
  const method = (init?.method ?? (input instanceof Request ? input.method : "GET")).toUpperCase();
  return handle(method, url, typeof init?.body === "string" ? init.body : null);
};
window.EventSource = DemoEventSource as unknown as typeof EventSource;

// The demo has no service worker to install.
try {
  Object.defineProperty(navigator.serviceWorker, "register", { value: () => new Promise(() => undefined) });
} catch {
  // A browser without service workers needs nothing here.
}

// Links to the printable reports open the pages written for the site.
document.addEventListener(
  "click",
  (e) => {
    const a = (e.target as Element | null)?.closest?.("a");
    if (!a) return;
    const url = new URL(a.href, location.href);
    if (!url.pathname.endsWith("/api/v1/reports")) return;
    e.preventDefault();
    const kind = url.searchParams.get("kind") === "esg" ? "esg" : "sci";
    const format = url.searchParams.get("format") ?? "html";
    if (format === "html") {
      window.open(new URL(`reports/${kind}-${lang(url)}.html`, here).href, "_blank", "noopener");
      return;
    }
    const link = document.createElement("a");
    link.href = new URL(`reports/${kind}.${format === "csv" ? "csv" : "json"}`, here).href;
    link.download = `greenlens-${kind}.${format === "csv" ? "csv" : "json"}`;
    link.click();
  },
  true,
);

// A notice says that the workspace is simulated.
function notice() {
  const n = document.createElement("p");
  n.setAttribute("role", "note");
  n.className = "demo-notice";
  n.textContent = catalogText("gui.demo.banner");
  Object.assign(n.style, {
    position: "fixed",
    right: "16px",
    bottom: "16px",
    margin: "0",
    padding: "8px 14px",
    borderRadius: "12px",
    background: "#0a1f17",
    color: "#e8f7ef",
    border: "1px solid #f2a541",
    fontFamily: "inherit",
    fontSize: "13px",
    lineHeight: "1.4",
    zIndex: "50",
    maxWidth: "min(400px, calc(100vw - 32px))",
  });
  document.body.appendChild(n);
  const update = () => {
    n.textContent = catalogText("gui.demo.banner");
  };
  new MutationObserver(update).observe(document.documentElement, {
    attributes: true,
    attributeFilter: ["lang"],
  });
}

if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", notice);
else notice();
