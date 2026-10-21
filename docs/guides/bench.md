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
- For `--warmup` (30 s by default) the prompts are sent without measuring,
  so the model is loaded and the processor and GPU reach a steady
  temperature and clock.
- Each of the `--runs` runs repeats the prompt set as many times as needed
  to last at least `--min-run` (60 s by default), the same number of times
  in every run. The power of a GPU changes between short runs even for the
  same work; one-minute runs average it out.
- The energy of each run is the energy attributed to the processes of the
  backend, sampled every 100 ms: their share of the processor and memory
  energy by CPU time, and their share of each GPU by its use per process.
  Other processes on the machine do not change it. With a calibration, only
  the energy above idle counts. When the processes of the backend cannot be
  found, the energy of the whole node is used and the result says so.
- Every run also records the energy of each domain of the node.

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
- Use longer runs with `--min-run`, or more runs, when the coefficient of
  variation stays high.

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
