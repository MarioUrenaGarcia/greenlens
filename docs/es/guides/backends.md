---
title: "Medir backends de inferencia y modelos"
description: "Deja que el agente encuentre tus servidores de inferencia, o decláralos, y consulta la energía de cada modelo."
weight: 45
---

El agente busca cada diez segundos los servidores de inferencia que corren
en el equipo y reparte la energía del nodo entre ellos y entre sus modelos.

## Detección automática

| Motor | Proceso reconocido | Puerto que prueba |
|---|---|---|
| Ollama | `ollama serve` | 11434 |
| llama.cpp | `llama-server` | `--port`, luego 9931 y 8080 |
| vLLM | `vllm serve`, `python -m vllm.entrypoints...` | `--port`, luego 8000 |
| TGI | `text-generation-launcher` | `--port`, luego 3000 |
| SGLang | `sglang serve`, `python -m sglang.launch_server` | `--port`, luego 30000 |
| LocalAI | `local-ai` | 8080 |

Solo se prueban los puertos en los que algo escucha. Un backend descubierto
se nombra por su motor y su puerto, como `vllm-8000`, así conserva su nombre
cuando se reinicia.

## Declarados en la configuración

Declara un backend cuando el descubrimiento no lo ve, por ejemplo en un
contenedor con su propia red, o para darle un nombre propio:

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

Un backend declarado se queda con el proceso del motor que escucha en el
puerto de su endpoint, o con los procesos del ejecutable indicado en
`process`. Un backend `process` no tiene endpoint: su energía se mide, sin
tokens. Con `discovery.enabled: false` solo se miden los backends
declarados.

## Tokens y modelos

| Motor | Tokens | Requiere |
|---|---|---|
| vLLM | por modelo | nada |
| SGLang | por modelo | `--enable-metrics` |
| llama.cpp | por modelo | `--metrics` |
| TGI | de su modelo | nada |
| Ollama, compatibles con OpenAI | el servidor no los cuenta | se conoce el modelo cargado |

`greenlens status` reporta `GL2002` para un servidor iniciado sin su opción
de métricas. La energía del backend se mide igual, pero no se reparte por
modelo. Cuando un backend sirve un solo modelo, toda su energía va a ese
modelo.

## Consultar los resultados

```bash
greenlens status
greenlens report --from 24h --group backend
greenlens report --from 7d --group model --output csv
```

La energía por token generado es la energía del modelo dividida entre los
tokens que generó, con el procesamiento del prompt incluido.
