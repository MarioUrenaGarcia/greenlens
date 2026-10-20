<p align="center">
  <img src="site/static/images/banner-en.svg" alt="GreenLens: energy observability for local AI inference" width="100%">
</p>

<p align="center">
  <a href="https://github.com/MarioUrenaGarcia/greenlens/actions/workflows/ci.yml"><img alt="CI status" src="https://github.com/MarioUrenaGarcia/greenlens/actions/workflows/ci.yml/badge.svg"></a>
  <a href="LICENSE"><img alt="License: AGPL-3.0-only" src="https://img.shields.io/badge/license-AGPL--3.0--only-34D399?style=flat-square"></a>
  <img alt="Go 1.27" src="https://img.shields.io/badge/Go-1.27-F2A541?style=flat-square">
  <img alt="Linux amd64 and arm64" src="https://img.shields.io/badge/Linux-amd64%20%7C%20arm64-9CC4AF?style=flat-square">
  <a href="https://mariourenagarcia.github.io/greenlens/"><img alt="Documentation on GitHub Pages" src="https://img.shields.io/badge/docs-GitHub%20Pages-0A1F17?style=flat-square"></a>
</p>

<p align="center">
  <b>Measure</b> the energy of every model. <b>Attribute</b> it to backends, models and teams. <b>Report</b> kWh, gCO2e, cost and SCI.
</p>

<p align="center">
  <a href="https://mariourenagarcia.github.io/greenlens/">Documentation</a> &nbsp;|&nbsp;
  <a href="https://mariourenagarcia.github.io/greenlens/docs/tutorials/first-steps/">First steps</a> &nbsp;|&nbsp;
  <a href="README.es.md">Leer en español</a>
</p>

---

GreenLens is an energy observability tool for AI models that run locally on
Linux, from a laptop to a fleet of servers. It is designed to read the energy
counters of CPUs, memory and GPUs, attribute that energy to each inference
backend and model, and report emissions, cost and the Software Carbon
Intensity (ISO/IEC 21031:2024).

<table>
  <tr>
    <td width="33%" valign="top">
      <img src="site/static/images/icons/measure.svg" width="40" height="40" alt=""><br>
      <b>Measure</b><br>
      Energy counters from the Linux kernel: RAPL, perf_event, hwmon and GPU
      drivers. Every value declares its quality: measured, attributed or
      estimated.
    </td>
    <td width="33%" valign="top">
      <img src="site/static/images/icons/attribute.svg" width="40" height="40" alt=""><br>
      <b>Attribute</b><br>
      The energy of the node is split between vLLM, TGI, llama.cpp, Ollama and
      other backends, then between models and teams, using CPU time, GPU usage
      and token counts.
    </td>
    <td width="33%" valign="top">
      <img src="site/static/images/icons/report.svg" width="40" height="40" alt=""><br>
      <b>Report</b><br>
      kWh, gCO2e, cost and SCI per thousand tokens, in the command line, a
      terminal interface, a graphical interface, Prometheus and OpenTelemetry.
    </td>
  </tr>
</table>

## Status

GreenLens measures the energy of the node and attributes it to inference
backends, models and the commands, processes and systemd units you choose:

- `greenlens doctor` finds every energy source, shows which ones are used and
  explains how to fix each problem;
- `greenlens setup-permissions` lets the agent read the sensors without root;
- `greenlens agent` samples every second, stores the data in SQLite and serves
  the other commands;
- `greenlens calibrate`, `measure`, `status` and `report` measure idle power,
  attribute energy and report kWh, gCO2e and cost;
- GPUs are measured through NVML, amdgpu, xe and i915, and the agent finds
  Ollama, llama.cpp, vLLM, TGI, SGLang and LocalAI servers and reports the
  energy of each backend and model, and per generated token;
- every message is available in English and Spanish, and every error has a
  code, a cause and a fix.

A proxy counts the tokens of Ollama and other OpenAI-compatible servers and
can return the energy of each request. Benchmarks and the graphical
interface belong to the design shown below.

<p align="center">
  <img src="site/static/images/terminal-en.svg" alt="Terminal session with greenlens: help, a command suggestion and an error with its cause and fix" width="820">
</p>

## Design

<p align="center">
  <img src="site/static/images/architecture-en.svg" alt="GreenLens architecture: energy sources feed the agent, which serves the command line, terminal and graphical interfaces, the API, Prometheus and OpenTelemetry" width="100%">
</p>

- **Local first.** Every value is computed and stored on your machine.
- **No telemetry.** No outbound connections by default.
- **Private.** The content of prompts and responses is never read, logged or
  stored.
- **Least privilege.** The agent runs without root and confines its own file
  access with Landlock.
- **Honest.** Every number comes with its unit, method and quality.

## Requirements

- Linux on amd64 or arm64 with kernel 4.19 or later.
- To build from source: Go at the version declared in `go.mod`, git and make.

## Installation

```bash
git clone https://github.com/MarioUrenaGarcia/greenlens.git
cd greenlens
make build
```

The binary is written to `bin/greenlens`. It is statically linked and has no
runtime dependencies, so it can be copied to any Linux machine of the same
architecture.

## Configuration

GreenLens works without a configuration file. To change a default, copy
[`deploy/config/greenlens.example.yaml`](deploy/config/greenlens.example.yaml)
to `/etc/greenlens/greenlens.yaml` or `~/.config/greenlens/greenlens.yaml`.
Every key can also be set with an environment variable, such as
`GREENLENS_AGENT_SAMPLE_INTERVAL=2s`. The most common settings:

| Key | Default | Meaning |
|---|---|---|
| `agent.sample_interval` | `1s` | How often energy is sampled |
| `attribution.idle_policy` | `dynamic` | How idle energy is attributed |
| `indicators.emission_factor_g_per_kwh` | FESEN 2024, 444 | Grid emission factor |
| `indicators.tariff.price_per_kwh` | none | Price of electricity, to report cost |

The language of the messages follows `--lang`, `GREENLENS_LANG` or the system
locale. The [configuration reference](https://mariourenagarcia.github.io/greenlens/docs/reference/configuration/)
lists every key.

## Usage

```bash
bin/greenlens doctor
sudo bin/greenlens setup-permissions --apply --user $USER
bin/greenlens agent
bin/greenlens calibrate
bin/greenlens measure -- ollama run llama3.2 "Why is the sky blue?"
bin/greenlens report --from 7d --by day
```

Every command accepts `--output json`, `csv` or `yaml` for scripts. Exit
codes: 0 success, 1 error, 2 incorrect usage, 3 no measurement source,
5 insufficient permissions, 6 invalid configuration. The
[first ten minutes](https://mariourenagarcia.github.io/greenlens/docs/tutorials/first-steps/)
tutorial walks through the whole setup.

## Documentation

The documentation site, in English and Spanish, is published at
<https://mariourenagarcia.github.io/greenlens/>. Its sources are in `docs/`
(English) and `docs/es/` (Spanish).

## Development

See [CONTRIBUTING.md](CONTRIBUTING.md). In short:

```bash
make tools
make hooks
make check
```

## License

GreenLens is Copyright (C) 2026 Mario Ureña García and is distributed under
the [GNU Affero General Public License v3.0 only](LICENSE). Organizations that
cannot meet the AGPL obligations can obtain a
[commercial license](COMMERCIAL-LICENSE.md). Third-party components and their
licenses are listed in [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).
