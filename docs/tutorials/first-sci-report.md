---
title: "Your first SCI report"
description: "Measure a model for a day, add the embodied carbon of the hardware and print the Software Carbon Intensity."
weight: 20
---

This tutorial produces the Software Carbon Intensity of a model served by
Ollama, from its first requests to a printed report. It assumes the agent
already runs, as in [First ten minutes](../first-steps/).

## 1. Count the tokens

The SCI divides emissions by generated tokens, so the agent must count them.
Ollama does not count tokens itself; turn on the proxy of GreenLens, restart
the agent, since proxy settings take effect at start, and send your
requests to the proxy instead of to Ollama:

```yaml
proxy:
  enabled: true
  listen: 127.0.0.1:11435
  upstream: http://127.0.0.1:11434
```

```bash
curl http://127.0.0.1:11435/api/generate -d '{"model": "qwen2.5:0.5b", "prompt": "Why is the sky blue?"}'
```

See [Count tokens with the proxy](../../guides/proxy/) for the details.

## 2. Look at the operational part

After some requests, the SCI of the node and of each model is already
there, without embodied carbon:

```bash
greenlens sci --from 1h --group model
```

The report says that the embodied carbon is not included and which keys it
lacks.

## 3. Add the embodied carbon

Look up the product carbon footprint that the maker of your server and of
your GPU publishes, and how many years you plan to use them. Then set them
in the configuration of the agent and reload it:

```yaml
indicators:
  sci:
    embodied:
      server_kgco2e: <embodied carbon of the server>
      gpu_kgco2e: <embodied carbon of the GPU>
      lifetime_years: <years of use>
```

```bash
sudo systemctl reload greenlens
```

## 4. Print the report

After a day of use:

```bash
greenlens sci --from 24h --group model --html sci.html
```

Open `sci.html` in a browser and print it. It shows O, M, R and the SCI of
the node and of each model, with the method. The same report is linked from
the reports screen of the interface, and
[Produce environmental and SCI reports](../../guides/reports/) describes the
environmental report beside it.
