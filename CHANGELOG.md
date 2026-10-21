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
