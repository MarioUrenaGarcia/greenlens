---
title: "Measure inference backends and models"
description: "Let the agent find your inference servers, or declare them, and read the energy of each model."
weight: 45
---

The agent finds the inference servers running on the machine every ten
seconds and splits the energy of the node between them and between their
models.

## Found automatically

| Engine | Process recognized | Port tried |
|---|---|---|
| Ollama | `ollama serve` | 11434 |
| llama.cpp | `llama-server` | `--port`, then 9931 and 8080 |
| vLLM | `vllm serve`, `python -m vllm.entrypoints...` | `--port`, then 8000 |
| TGI | `text-generation-launcher` | `--port`, then 3000 |
| SGLang | `sglang serve`, `python -m sglang.launch_server` | `--port`, then 30000 |
| LocalAI | `local-ai` | 8080 |

Only ports that something listens on are tried. A discovered backend is
named after its engine and port, such as `vllm-8000`, so it keeps its name
when it restarts.

## Declared in the configuration

Declare a backend when discovery cannot see it, for example in a container
with its own network, or to give it a name of your own:

```yaml
backends:
  - name: chat
    kind: vllm
    endpoint: http://127.0.0.1:8000
  - name: embeddings
    kind: tgi
    endpoint: http://127.0.0.1:3000
    process: text-generation-launcher
  - name: batch
    kind: process
    process: python3
```

A declared backend owns the engine process listening on the port of its
endpoint, or the processes of the executable named in `process`. A
`process` backend has no endpoint: its energy is measured, without tokens.
Set `discovery.enabled: false` to measure only the declared backends.

## Tokens and models

| Engine | Tokens | Needs |
|---|---|---|
| vLLM | per model | nothing |
| SGLang | per model | `--enable-metrics` |
| llama.cpp | per model | `--metrics` |
| TGI | for its model | nothing |
| Ollama, OpenAI-compatible | not counted by the server | the loaded model is known |

`greenlens status` reports `GL2002` for a server started without its
metrics option. The energy of a backend is then measured but not split by
model. When a backend serves a single model, all its energy goes to it.

## Reading the results

```bash
greenlens status
greenlens report --from 24h --group backend
greenlens report --from 7d --group model --output csv
```

The energy per generated token is the energy of the model divided by the
tokens it generated, prompt processing included.
