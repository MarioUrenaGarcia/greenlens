# Changelog

All notable changes to GreenLens are recorded in this file. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and versions follow
[Semantic Versioning](https://semver.org/).

## [Unreleased]

### Added

- Energy measurement of the node from powercap RAPL, perf_event, hwmon
  processor drivers, ACPI power meters, IPMI DCMI, laptop batteries and, when
  nothing can be read, a CPU model, choosing one source per domain so energy
  is never counted twice.
- GPU energy from the hwmon sensors of amdgpu, xe and i915, chosen per card
  by PCI address. The sensor of an AMD APU measures the package when RAPL
  cannot be read.
- NVIDIA GPU energy through NVML, loaded by the greenlens-nvml helper so the
  main binary stays static, with nvidia-smi as a fallback.
- Energy per inference backend and model: the agent finds Ollama,
  llama.cpp, vLLM, TGI, SGLang and LocalAI servers or uses the declared
  ones, attributes processor, memory and GPU energy to each, splits it
  between models by tokens, and stores it for `status`, `report --group`
  and the API.
- The energy of a prompt token and of a generated token of each model,
  fitted by least squares over the last day with 95% confidence intervals
  and reported when R2 reaches `attribution.regression_min_r2`.
- A token-counting reverse proxy, run by the agent, for Ollama and other
  OpenAI-compatible servers: it forwards requests unchanged, reads only the
  token counts of the responses, can return the energy of each request in
  `X-GreenLens-Energy-Joules`, and attributes energy to consumers identified
  by an HMAC of a request header. `greenlens proxy` shows its state.
- `greenlens bench`: reproducible benchmarks of a model with greedy
  decoding and a fixed seed, reporting the energy per generated token with
  its mean, deviation, coefficient of variation and 95% confidence interval,
  the conditions of the machine and the likely causes of unstable runs; only
  the hash of the prompts is stored.
- `greenlens compare`: compares two benchmarks or two models and projects
  the energy, emissions and cost saved by switching.
- `greenlens top`: a live terminal view of the node, the backends and
  models, the consumers and the energy sources, with sorting, filtering,
  pause and CSV export; it works from 80 by 24 characters, in ASCII and
  without color.
- `greenlens explain` describes every value GreenLens reports, with its
  formula, unit, method and limits; `greenlens help <topic>` explains
  measurement, attribution, sources, permissions, privacy and configuration;
  `greenlens completion` prints completion scripts for bash, zsh and fish.
- Manual pages in English and Spanish, `greenlens(1)`, one per command and
  `greenlens.yaml(5)`, written by `make man` in plain ASCII roff.
- `greenlens backup` and `restore`: consistent copies of the database through
  the SQLite backup interface, made by the running agent or from the file;
  a restore checks the backup first, refuses while the agent runs, keeps the
  previous database and updates older schemas.
- `greenlens export energy`, `usage` and `benchmarks`: CSV, JSON or YAML with
  the same column names in every language and values in base units.
- `greenlens license`: the license, the source code of the running version
  as section 13 of the AGPL asks, and the third-party components, with their
  full texts on request.
- An HTTP API on `127.0.0.1:10060` for programs and browsers, with the
  viewer, operator and admin roles, access tokens kept as argon2id hashes
  and managed with `greenlens token`, browser sessions with CSRF
  protection, an audit log, security headers, rate limits and optional TLS.
  The agent refuses to listen beyond loopback while no token exists.
- The graphical interface, served by the agent: a green glass design with
  dark and light themes, the live power of the node stacked by domain with
  a table alternative, the energy, emissions and cost of a period, the
  models that used the most energy, sign-in with a one-time link or a
  token, and an efficient mode without blur or animations that turns on by
  itself on battery. Text keeps a contrast of 4.5:1 over the brightest
  background, and reduced motion, reduced transparency and more contrast are
  followed. The sources screen shows every energy channel, whether it is
  used and why, the problems with their fixes, the calibration and the
  agent itself; the about screen shows the license, the source code of the
  running version and the estimated consumption of the interface, which
  turns the efficient mode on above 1 W. The models screen lists the energy, J per
  token, Wh per thousand tokens and tokens per joule of every model in a
  period, with the history of each one; the backends screen shows each
  server with its processes, power and models; the consumers screen shows
  the energy of each team or application for showback.
- `/api/v1/usage` splits the use by backend, model or consumer in buckets,
  and the backends carry their root processes.
- Queries of long periods read every whole hour that falls inside one
  bucket from the hourly rollups and every whole minute from the minute
  rollups, with new indexes by resolution, so a query of 30 days reads
  about 720 rows per series instead of 43,200 and returns the same sums as
  the raw rows.
- Benchmarks from the interface: the agent runs one at a time with the
  defaults of `greenlens bench`, measures it with its own samples and the
  energy it attributes to the backend, reports its progress on the live
  stream and can cancel it. The benchmarks screen lists the stored ones,
  compares two with the energy, emissions and cost saved over a number of
  tokens, and exports them as CSV or JSON. `POST /api/v1/benchmarks` now
  starts a benchmark; results measured by `greenlens bench` go to
  `/api/v1/benchmarks/results`.
- The configuration in the interface, for administrators: every key with
  its description, default and environment variable, a checked preview of
  the resulting file, and applying it when the agent can write its file,
  keeping its comments and reloading it. The system service, whose file
  is read-only, gets the file to download and the commands to install it.
- First steps in the interface, opened once on a node without a
  calibration and skippable at every step: the sources found and their
  quality, an idle calibration that the agent measures with its own
  samples, and the emission factor and the tariff.
  `POST /api/v1/calibrations/run` starts that calibration, which GET and
  DELETE follow and cancel; it is refused while a benchmark runs, and
  the other way around.
- Help in the interface: the documentation of the site, in English and
  Spanish, is embedded in the binary, rendered without raw HTML and
  searchable, and every panel links to its page; links between pages and
  anchors work as routes of the interface. `GET /api/v1/help` serves the
  index and the search, and `GET /api/v1/help/{page}` each page.
- Energy reports in the interface: energy, emissions, cost and water of a
  period, by domain per day or hour and by backend, model or consumer,
  printable from the browser and exported as CSV or JSON.
- `greenlens gui` opens the interface in the browser with a one-time
  sign-in link, prints the link with the SSH port forward when run over
  SSH, and stops with GL4015 when the HTTP API is off. A desktop launcher
  and its icon are in `deploy/desktop`; started from it, errors appear as
  desktop notifications.
- The interface can be installed as an application from the browser: a
  manifest, own icons and a service worker that keeps only the files of
  the interface, so the window opens and says that the agent does not
  answer; measurements are never kept by the browser.
- A live stream of server-sent events on `/api/v1/stream`: the power of
  every domain with each sample and the use of the backends with each
  usage interval, resumable with `Last-Event-ID` from the last 15 minutes.
- An OpenAPI 3.1 description of the HTTP API in `api/openapi.yaml`, served
  at `/api/v1/openapi.yaml`, with Spanish translations of every text. Tests
  check that it lists every route with its role and that the documents of
  the agent match its schemas; the API reference is generated from it.
- Prometheus metrics on `/metrics`: node energy and power by domain, source
  and quality, idle and unattributed energy, energy of each backend, model
  and consumer, tokens, energy per generated token, emissions, cost and
  water when configured, the state of every source and the errors and
  durations of every collector.
- `greenlens agent`: samples every second, stores the data in SQLite with
  one-minute and one-hour rollups, serves a local API on a Unix socket and
  confines itself with Landlock.
- `greenlens doctor`, `setup-permissions`, `calibrate`, `measure`, `status`,
  `report` and `config`, with table, JSON, CSV and YAML output.
- Attribution of energy to commands, processes and systemd units by CPU
  share, with the dynamic, proportional, full and wall idle policies.
- Emissions with the FESEN 2024 factor by default, and cost and water when
  configured.
- A hardened systemd unit, udev rules and an example configuration.
- `greenlens version` shows the version, source revision, Go version and
  platform of the binary.
- Messages in English and Spanish, selected with `--lang`, `GREENLENS_LANG` or
  the system locale.
- Every command line error has a stable code, a cause and a suggested fix.
- Help text wraps to fit an 80-column terminal.
- Documentation site in English and Spanish, published with GitHub Pages,
  with a generated error code reference.
