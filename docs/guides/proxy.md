---
title: "Count tokens with the proxy"
description: "Measure the energy per token and per consumer of Ollama and other servers that do not count tokens."
weight: 47
---

Ollama and many OpenAI-compatible servers do not publish token counters.
The agent can run a reverse proxy in front of them that reads the token
counts of every response as it passes.

## What the proxy reads

The proxy forwards every request and response unchanged, streamed responses
included. From the responses it reads only the model name and the token
counts: `prompt_eval_count` and `eval_count` from Ollama, `usage` from
OpenAI-compatible servers, or the number of streamed pieces when a stream
carries no final count. It never reads request bodies, and it never stores
or logs prompts, responses or keys; the test suite checks that none of them
reaches the logs or the data directory.

## Turn it on

```yaml
proxy:
  enabled: true
  listen: 127.0.0.1:11435
  upstream: http://127.0.0.1:11434
```

Reload the agent and point your clients at `http://127.0.0.1:11435` instead
of the server. `greenlens proxy` shows its state. The tokens counted by the
proxy are used for backends that do not count them themselves; for vLLM,
TGI, SGLang and llama.cpp their own counters are kept, so no token is
counted twice.

## Energy per request

With `per_request_energy_header: true`, every response carries
`X-GreenLens-Energy-Joules`: the energy of the backend while the request
ran, split between the requests in flight by the output each one streamed,
or in equal parts while prompts are processed. Streamed responses carry it
as an HTTP trailer, since it is known only at the end.

## Consumers

`consumer_header` names the request header that tells consumers apart, such
as `Authorization` or `X-Api-Key`. Its value is never stored: a consumer is
recorded by the HMAC of the value with a salt kept in the data directory,
and named `consumer-` followed by the start of that HMAC. To give consumers
names, list the SHA-256 of their header value in `consumer_aliases_file`:

```bash
printf '%s' 'Bearer sk-example' | sha256sum
```

```yaml
team-a: sha256:<the 64 hexadecimal digits printed above>
```

Then `greenlens report --group consumer` shows the energy of each one.
