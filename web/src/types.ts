// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

// Documents of the HTTP API, as described in api/openapi.yaml.

export type Role = "viewer" | "operator" | "admin";

export interface Session {
  schema_version: number;
  role: Role;
  actor: string;
  via: "socket" | "session" | "token";
  csrf?: string;
}

export interface Catalog {
  schema_version: number;
  lang: string;
  languages: string[];
  messages: Record<string, string>;
}

export interface Problem {
  code: string;
  args?: Record<string, string>;
  channel?: string;
  backend?: string;
}

export interface Domain {
  domain: string;
  level: "component" | "system" | "wall";
  power_w: number;
  energy_j: number;
  quality: string;
}

export interface Channel {
  id: string;
  source: string;
  domain: string;
  instance?: string;
  quality: string;
  method: string;
  label?: string;
  selected: boolean;
  reason: string;
  reason_args?: Record<string, string>;
}

export interface Factors {
  emission_g_per_kwh: number;
  emission_source: string;
  pue: number;
  tariff_per_kwh?: number;
  currency: string;
  wue_l_per_kwh?: number;
}

export interface Regression {
  prompt_j_per_token: number;
  prompt_ci95: number;
  generated_j_per_token: number;
  generated_ci95: number;
  base_w: number;
  base_ci95: number;
  r2: number;
  intervals: number;
  method: string;
}

export interface Model {
  name: string;
  energy_j: number;
  prompt_tokens: number;
  generated_tokens: number;
  requests: number;
  joules_per_generated_token: number;
  method: string;
  quality: string;
  regression?: Regression;
}

export interface Backend {
  name: string;
  kind: string;
  endpoint?: string;
  static: boolean;
  container_id?: string;
  processes: number;
  roots?: { pid: number; name: string }[];
  version?: string;
  token_counters: boolean;
  last_read?: string;
  problem?: Problem;
  power_w: number;
  energy_j: number;
  cpu_energy_j: number;
  dram_energy_j: number;
  gpu_energy_j: number;
  models?: Model[];
}

export interface Backends {
  schema_version: number;
  interval_ms: number;
  backends: Backend[];
}

export interface Status {
  schema_version: number;
  version: string;
  started_at: string;
  interval_ms: number;
  sample_time: string;
  samples: number;
  estimated: boolean;
  domains: Domain[];
  channels: Channel[];
  problems?: Problem[];
  calibration?: {
    at: string;
    baseline: { cpu_w: number; dram_w: number; platform_w: number; gpu_w?: Record<string, number> };
    idle_now_w?: number;
    drift?: number;
  };
  self: { energy_j: number; cpu_seconds: number; rss_bytes: number };
  idle_policy: string;
  factors: Factors;
  cpu_joules_per_busy_second: number;
  backends?: Backend[];
}

export interface LivePoint {
  time: string;
  interval_ms: number;
  power_w: Record<string, number>;
}

export interface ChannelEnergy {
  bucket_start: string;
  channel: string;
  source: string;
  domain: string;
  quality: string;
  energy_j: number;
  samples: number;
}

export interface Usage {
  schema_version: number;
  from: string;
  to: string;
  bucket_ms: number;
  resolution: string;
  rows: ChannelEnergy[];
}

export interface UsageEnergy {
  bucket_start?: string;
  backend: string;
  kind: string;
  model?: string;
  consumer?: string;
  energy_j: number;
  prompt_tokens: number;
  generated_tokens: number;
  requests: number;
  tokens_counted: boolean;
}

export interface BackendUsage {
  schema_version: number;
  from: string;
  to: string;
  group_by: "backend" | "model" | "consumer";
  bucket_ms: number;
  resolution: string;
  rows: UsageEnergy[];
}

export interface ErrorBody {
  error: string;
}

export interface Summary {
  n: number;
  mean: number;
  stddev: number;
  cv: number;
  ci95: number;
}

export interface Benchmark {
  id?: number;
  started_at: string;
  ended_at: string;
  target: { kind: string; endpoint: string; model: string };
  params: {
    max_tokens: number;
    temperature: number;
    seed: number;
    runs: number;
    warmup_s: number;
    min_run_s: number;
    passes: number;
  };
  prompt_set_sha256: string;
  prompts: number;
  context: { cpu?: string; gpus?: string[]; governor?: string; other_cpu_share: number; throttled?: boolean };
  runs: { energy_j: number; duration_s: number; generated_tokens: number; j_per_generated_token: number }[];
  basis: "backend" | "dynamic" | "total";
  calibrated: boolean;
  energy_j: Summary;
  j_per_generated_token: Summary;
  generated_tokens: number;
  causes?: string[];
}

export interface BenchmarkRequest {
  kind: string;
  endpoint?: string;
  model: string;
  runs: number;
  max_tokens: number;
  warmup_s: number;
  min_run_s: number;
}

export interface BenchmarkJob {
  schema_version: number;
  state: "idle" | "running" | "done" | "failed" | "canceled";
  run: number;
  runs: number;
  request?: BenchmarkRequest;
  started_at?: string;
  ended_at?: string;
  benchmark_id?: number;
  error?: string;
}

export interface CalibrationJob {
  schema_version: number;
  state: "idle" | "running" | "done" | "failed" | "canceled";
  duration_s: number;
  started_at?: string;
  ended_at?: string;
  result?: { samples: number; domains: Record<string, { p10_w: number; p50_w: number; p90_w: number }> };
  error?: string;
}
