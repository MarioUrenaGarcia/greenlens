Responses of inference backends used by the connector, proxy and benchmark
tests.

- `<kind>/<version>/` holds responses captured from that version of the
  engine. The only changes are those that keep the machine anonymous: the
  directory of the model file is replaced by `/models/`, the random media
  marker of llama-server by a fixed one, and times in the local zone by
  UTC.
  - `llamacpp/b11669` (release v0.7.0) and `llamacpp/b10416`: llama-server
    built for CPU, serving qwen2.5-0.5b-instruct-q4_k_m.gguf, after two chat
    requests; `metrics_disabled.txt` is the 501 answer without `--metrics`.
  - `ollama/0.40.6`: with no model loaded (`api_ps.json`) and with
    qwen2.5:0.5b loaded, with the responses of the native and the
    OpenAI-compatible APIs to the prompt "Name one primary color.".
- `<kind>/docs/` holds responses written from the official documentation of
  the engine, for engines not yet captured: metric names and labels follow
  vllm/v1/metrics/loggers.py (vLLM), docs/references/production_metrics.mdx
  (SGLang), tools/server/README.md (llama.cpp) and
  docs/source/reference/metrics.md (TGI). Values are arbitrary.
