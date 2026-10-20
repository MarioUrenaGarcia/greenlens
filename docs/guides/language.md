---
title: "Choose the language"
description: "Select English or Spanish for every message."
weight: 10
---

GreenLens shows every message, help text and error in English or Spanish. It
uses the first of these settings that is defined:

| Setting | Example |
|---|---|
| `--lang` flag | `greenlens --lang es version` |
| `GREENLENS_LANG` environment variable | `GREENLENS_LANG=es greenlens version` |
| System locale: `LC_ALL`, `LC_MESSAGES` or `LANG` | `LANG=es_MX.UTF-8` |

Accepted values are `en`, `es` and `auto`, which uses the system locale. The
locale variables follow the POSIX order: the first one that is set decides.

An unsupported value in `--lang` or `GREENLENS_LANG` is an error with code
[GL0008](../../reference/errors/#gl0008), shown in the language of the system
locale. An unsupported system locale, or the `C` locale, falls back to
English without an error.
