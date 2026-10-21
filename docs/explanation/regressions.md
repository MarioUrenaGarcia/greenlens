---
title: "Regressions of the energy per token"
description: "How GreenLens judges each day of a model against the days before it, and why it shows the changes of context."
weight: 45
---

The energy per generated token of a model moves with the load, the length
of the prompts and the temperature of the machine. A new version of the
engine, a GPU driver or a different file under the same model name can move
it for good. GreenLens tells the two apart day by day.

## The test

For every local day in which a model generated at least
`analysis.regression.min_generated_tokens` tokens, GreenLens takes its
energy per generated token, x, and the same value of each of the
`analysis.regression.baseline_days` calendar days before it that also had
enough tokens. With at least `analysis.regression.min_baseline_days` of
them, it computes their mean, m, and sample standard deviation, s:

```
z = (x - m) / max(s, 0.005 x m)
```

A day with z of `analysis.regression.z_threshold` or more is a regression; a
day with z of minus that threshold or less is an improvement. The floor of
0.5% of the mean keeps a perfectly steady baseline from turning every small
change into a regression. Days are those of `indicators.timezone`.

## A worked case

Seven baseline days at 0.20, 0.21, 0.19, 0.20, 0.22, 0.18 and 0.20 J per
token have a mean of 0.20 J. Their deviations from it, squared, add up to
0.0010, so s = sqrt(0.0010 / 6) = 0.012910 J. A day at 0.25 J has
z = 0.05 / 0.012910 = 3.873: a regression with the default threshold of 3.
A day at 0.21 J has z = 0.775 and is normal; one at 0.15 J has z = -3.873
and is an improvement.

## Changes of context

The agent records when something the measurements depend on changes: its
own version, the kernel, the NVIDIA driver, the version of each backend,
the file of each model when the engine reports its digest (Ollama does),
and the configuration. The models screen lists these changes next to the
days of a model, and `greenlens report --regressions` prints them after
the days that stand out, so a regression can be traced to its cause.

Regressions are reported, not alerted; notifications belong to the alerts.
