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
- Durante `--warmup` (30 s por defecto) se envían los prompts sin medir, para
  que el modelo esté cargado y el procesador y la GPU alcancen una
  temperatura y una frecuencia estables.
- Cada una de las `--runs` corridas repite el conjunto de prompts las veces
  necesarias para durar al menos `--min-run` (60 s por defecto), el mismo
  número de veces en todas. La potencia de una GPU cambia entre corridas
  cortas aun con el mismo trabajo; las corridas de un minuto lo promedian.
- La energía de cada corrida es la atribuida a los procesos del backend,
  muestreada cada 100 ms: su parte de la energía del procesador y de la
  memoria según su tiempo de CPU, y su parte de cada GPU según el uso de cada
  proceso. Los demás procesos del equipo no la cambian. Con una calibración,
  solo cuenta la energía sobre el reposo. Cuando no se encuentran los
  procesos del backend, se usa la energía de todo el nodo y el resultado lo
  indica.
- Cada corrida registra también la energía de cada dominio del nodo.

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
- Usa corridas más largas con `--min-run`, o más corridas, cuando el
  coeficiente de variación sigue alto.

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
