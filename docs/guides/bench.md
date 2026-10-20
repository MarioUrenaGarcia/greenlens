---
title: "Benchmark and compare models"
description: "Measure the energy per generated token of a model reproducibly, and project the savings of another one."
weight: 48
---

`greenlens bench` sends the same prompts to a model several times and
measures the energy of each run.

```bash
greenlens calibrate
greenlens bench --model qwen2.5:0.5b
greenlens bench --kind llamacpp --endpoint http://127.0.0.1:8080 --model q.gguf --runs 10
```

## How a benchmark runs

- The prompts are a built-in set of eight everyday tasks, or a file with one
  prompt per line given with `--prompts`. Only their SHA-256 is stored.
- Generation is greedy, temperature 0, with seed 42 and at most
  `--max-tokens` tokens per prompt, through the native API of Ollama or the
  OpenAI-compatible chat API of the other engines.
- One warmup pass loads the model; then each of the `--runs` passes is
  measured from the energy counters, sampled every 100 ms.
- With a calibration, the energy per token is the energy above idle;
  without one, it includes idle power and the result says so.

The result gives, for the energy per generated token, the mean, the
standard deviation, the coefficient of variation and the 95% confidence
interval from Student's t. It records the processor, GPUs, kernel, NVIDIA
driver, governor, temperatures and throttling. When the runs vary by more
than 10%, it lists the likely causes: a governor other than `performance`,
a processor that warmed up or throttled, or other processes using the CPU.

## Stable results

- Set the governor to `performance` while benchmarking.
- Close other workloads; the result reports the share of CPU time that was
  not the backend.
- Use more runs, or longer prompts, when the coefficient of variation stays
  high.

## Compare

With the agent running, every benchmark is saved and gets an id:

```bash
greenlens compare 3 7
greenlens compare ollama-11434/llama3.2 ollama-11434/qwen2.5:0.5b --from 7d
```

Two benchmarks are compared per 1,000 generated tokens, with a warning when
their confidence intervals overlap or their prompts, hardware or settings
differ. Two models are compared by the energy per generated token of their
real traffic in the period, and the projection assumes the second model
would answer the same requests with as many tokens.
