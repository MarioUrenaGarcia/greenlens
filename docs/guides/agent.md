---
title: "Run the agent"
description: "Run the agent as a system service or as a user, and manage its data."
weight: 30
---

The agent measures continuously. Every other command can run without it,
except `status` and `report`, which read what it stored.

## As a system service

Install the binary as `/usr/bin/greenlens`, then the files from `deploy/`:

```bash
sudo install -m 0644 deploy/systemd/greenlens.sysusers /usr/lib/sysusers.d/greenlens.conf
sudo systemd-sysusers
sudo install -D -m 0644 deploy/config/greenlens.example.yaml /etc/greenlens/greenlens.yaml
sudo install -m 0644 deploy/systemd/greenlens.service /etc/systemd/system/greenlens.service
sudo greenlens setup-permissions --apply
sudo systemctl daemon-reload
sudo systemctl enable --now greenlens
```

The service runs as the unprivileged `greenlens` user with a hardened
systemd configuration, keeps its database in `/var/lib/greenlens` and
listens on `/run/greenlens/greenlens.sock`. Members of the `greenlens` group
can use `greenlens status` and `greenlens report`.

The agent also confines itself with Landlock on kernels that support it: it
can only read `/sys`, `/proc` and its configuration, and only write its data
and socket directories.

| Task | Command |
|---|---|
| Reload the configuration | `sudo systemctl reload greenlens` |
| Follow the logs | `journalctl -u greenlens -f` |
| Stop | `sudo systemctl stop greenlens` |

By default the service may only connect to localhost. The
`deploy/systemd/greenlens-network.conf` drop-in allows the internal networks
of your backends or meters.

## As a user, without installation

```bash
greenlens agent
```

The database goes to `$XDG_DATA_HOME/greenlens` and the socket to
`$XDG_RUNTIME_DIR/greenlens/greenlens.sock`. `greenlens status` finds the
agent of the user before the system service.

## Data

| Resolution | Kept by default | Setting |
|---|---|---|
| Every sample | 7 days | `retention.raw` |
| One minute | 90 days | `retention.rollup_1m` |
| One hour | 2 years | `retention.rollup_1h` |

The database is SQLite in WAL mode, readable only by its owner. The agent
checks its integrity every day and returns free space every hour.
