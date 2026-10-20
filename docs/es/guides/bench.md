---
title: "Medir y comparar modelos"
description: "Mide de forma reproducible la energía por token generado de un modelo y proyecta el ahorro de otro."
weight: 48
---

`greenlens bench` envía los mismos prompts a un modelo varias veces y mide la
energía de cada corrida.

```bash
greenlens calibrate
greenlens bench --model qwen2.5:0.5b
greenlens bench --kind llamacpp --endpoint http://127.0.0.1:8080 --model q.gguf --runs 10
```

## Cómo corre un benchmark

- Los prompts son un conjunto incluido de ocho tareas cotidianas, o un
  archivo con un prompt por línea indicado con `--prompts`. Solo se guarda su
  SHA-256.
- La generación es determinista, temperatura 0, con semilla 42 y a lo más
  `--max-tokens` tokens por prompt, por la API nativa de Ollama o la API de
  chat compatible con OpenAI de los demás motores.
- Una pasada de calentamiento carga el modelo; después cada una de las
  `--runs` pasadas se mide con los contadores de energía, muestreados cada
  100 ms.
- Con una calibración, la energía por token es la que está sobre el reposo;
  sin ella, incluye la potencia en reposo y el resultado lo indica.

El resultado da, para la energía por token generado, la media, la desviación
estándar, el coeficiente de variación y el intervalo de confianza al 95% de
la t de Student. Registra el procesador, las GPU, el kernel, el driver de
NVIDIA, el governor, las temperaturas y la reducción de frecuencia por
temperatura. Cuando las corridas varían más de 10%, lista las causas
probables: un governor distinto de `performance`, un procesador que se
calentó o redujo su frecuencia, u otros procesos usando la CPU.

## Resultados estables

- Pon el governor en `performance` mientras mides.
- Cierra otras cargas de trabajo; el resultado reporta la parte del tiempo de
  CPU que no fue del backend.
- Usa más corridas, o prompts más largos, cuando el coeficiente de variación
  sigue alto.

## Comparar

Con el agente en ejecución, cada benchmark se guarda y recibe un id:

```bash
greenlens compare 3 7
greenlens compare ollama-11434/llama3.2 ollama-11434/qwen2.5:0.5b --from 7d
```

Dos benchmarks se comparan por cada 1,000 tokens generados, con un aviso
cuando sus intervalos de confianza se traslapan o difieren sus prompts, su
hardware o su configuración. Dos modelos se comparan por la energía por
token generado de su tráfico real en el periodo, y la proyección supone que
el segundo modelo respondería las mismas peticiones con los mismos tokens.
