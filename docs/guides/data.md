---
title: "Back up, restore and export data"
description: "Keep a copy of the database, move it to another machine and hand measurements to other tools."
weight: 55
---

The agent keeps every measurement in one SQLite database, `greenlens.db`,
in its data directory (`agent.data_dir`, `/var/lib/greenlens` for the system
service).

## Back up

```bash
greenlens backup greenlens-2026-10-20.db
```

When the agent is running it makes the copy itself through the SQLite backup
interface, so measurements continue and you need no access to its data
directory, only to its socket. The copy reflects a single moment, is checked
before it gets its name, is readable only by you, and never replaces an
existing file. Without a running agent, the database of your user is copied;
`--database` names another one:

```bash
sudo -u greenlens greenlens backup --database /var/lib/greenlens/greenlens.db /tmp/greenlens.db
```

## Restore

The agent must be stopped, since it keeps the database open:

```bash
sudo systemctl stop greenlens
sudo -u greenlens greenlens restore --database /var/lib/greenlens/greenlens.db /tmp/greenlens.db
sudo systemctl start greenlens
```

`greenlens restore` first checks that the file is a complete GreenLens
database that this version can read; if it is not, nothing changes. The
current database is then copied next to it as
`greenlens.db.before-restore-<time>`, unless you pass `--no-keep`, and the
backup replaces it. A backup made by an older version is updated to the
current schema; one made by a newer version is refused with GL4010.

## Export

`greenlens export` writes CSV by default, or JSON and YAML with `--output`.
Column and field names are the same in every language and values are in base
units, joules and tokens, so scripts do not depend on the language of the
user.

```bash
greenlens export energy --from 7d --bucket 1h > energy.csv
greenlens export usage --from 30d --group consumer > consumers.csv
greenlens export usage --from 7d --bucket 24h --group model --output json
greenlens export benchmarks --output json > benchmarks.json
```

| Data | Rows |
|---|---|
| `energy` | energy of every channel per bucket, with its source, domain, quality and number of samples |
| `usage` | energy, tokens and requests of each backend, model or consumer, for the period or per bucket |
| `benchmarks` | every stored benchmark with its energy per generated token and statistics; the JSON output keeps every run |

CSV times are in UTC; JSON and YAML times carry their offset. Exports read the
finest resolution still kept for the start of the period: raw samples for the
last 7 days, one-minute rollups for 90 days and one-hour rollups for 2 years
by default.
