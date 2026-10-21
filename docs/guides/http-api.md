---
title: "Use the HTTP API and access tokens"
description: "Reach the API and the interface from programs and other machines, with roles and access tokens."
weight: 57
---

The agent serves its HTTP API, the graphical interface and the Prometheus
metrics on `127.0.0.1:10060`. Commands on the same machine use the Unix
socket instead and need no token.

## Who may do what

Every request needs a session or an access token, except `/healthz`,
`/readyz` and `/metrics`. Loopback is no exception, since every local user
can reach `127.0.0.1`.

| Role | May |
|---|---|
| `viewer` | read measurements, reports, backends, sources and benchmarks |
| `operator` | also run benchmarks and calibrations |
| `admin` | also manage tokens, read the audit log and download backups |

Every action of an operator or an administrator is recorded in the audit log
with the token or the local user behind it.

## Access tokens

```bash
greenlens token create --name grafana --role viewer --expires 90d
greenlens token list
greenlens token revoke grafana
```

The secret is printed once; GreenLens keeps only its argon2id hash, in
`tokens` in the data directory (`server.tokens_file` names another file).
Send it as a bearer token:

```bash
curl -H "Authorization: Bearer $GREENLENS_TOKEN" http://127.0.0.1:10060/api/v1/status
```

With the agent running, `greenlens token` goes through it, so the change
appears in the audit log at once. Without an agent it changes the file
directly; for the system service run it as the user of the service:

```bash
sudo -u greenlens greenlens token create --name admin --role admin
```

## Listening on the network

Set `server.listen` to an address of the machine, such as `192.0.2.10:10060`,
and allow the address in the network drop-in of the systemd unit. The agent
refuses to start on an address beyond loopback while there is no access
token, so create one first. Give the API a certificate with
`server.tls.cert_file` and `server.tls.key_file`; without TLS, tokens and
session cookies cross the network in clear.

From another machine, an SSH tunnel keeps the API on loopback:

```bash
ssh -L 10060:127.0.0.1:10060 server.example.com
```

## Browser sessions

A browser signs in with a token on the sign-in page, or with a one-time link
that the agent gives to local users. The session cookie is `HttpOnly` and
`SameSite=Strict`, and `Secure` with TLS. A session ends after a week
without use and after 30 days in any case. Requests of a session that change
something must carry its CSRF token and come from the same origin.

## Limits

Each address may send 30 requests per second, with bursts of 60, and 10
sign-in attempts per minute. Request bodies are limited to 1 MiB and headers
to 32 KiB.
