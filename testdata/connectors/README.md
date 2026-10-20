Responses of inference backends used by the connector tests.

- `<kind>/<version>/` holds responses captured from that version of the
  engine, with nothing removed except values that identify the machine.
- `<kind>/docs/` holds responses written from the official documentation of
  the engine, for engines not yet captured: metric names and labels follow
  vllm/v1/metrics/loggers.py (vLLM), docs/references/production_metrics.mdx
  (SGLang), tools/server/README.md (llama.cpp) and
  docs/source/reference/metrics.md (TGI). Values are arbitrary.
