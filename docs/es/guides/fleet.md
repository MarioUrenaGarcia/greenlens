---
title: "Leer una flota de agentes con TLS mutuo"
description: "Crea la autoridad certificadora de una flota, da un certificado a cada agente y léelos todos desde un solo lugar."
weight: 58
---

Cada máquina corre su propio agente. Para ver varias juntas, un lector
central, el hub, recoge sus métricas por la red interna. El TLS mutuo
protege ese camino en los dos sentidos: el agente prueba qué nodo es, y
solo el hub puede leerlo. No viaja ningún token ni hay que copiarlo a cada
nodo.

## La autoridad de la flota

Créala una vez, en una máquina que no sea un nodo:

```bash
greenlens hub init-ca --dir /etc/greenlens/fleet-ca
```

Esto escribe `ca.crt` y `ca.key`. El certificado va a cada agente y al hub.
La llave se queda donde se creó: quien la tenga puede emitir certificados
en los que confía toda la flota. La autoridad es válida cinco años, salvo
que `--days` diga otra cosa, y el comando nunca reemplaza una existente.

## Un certificado para cada agente

```bash
greenlens hub issue-cert --ca-dir /etc/greenlens/fleet-ca --out ./certs \
  --name node-a --host 10.0.0.11 --host node-a.example.org
```

`--name` identifica al nodo. `--host` lista cada dirección y nombre de host
que el hub usa para alcanzarlo; el hub rechaza a un agente cuyo certificado
no nombra la dirección a la que se conectó. Copia `node-a.crt`,
`node-a.key` y `ca.crt` al nodo, legibles solo por el usuario del agente, y
nómbralos en su configuración:

```yaml
server:
  listen: 10.0.0.11:10060
  tls:
    cert_file: /etc/greenlens/node-a.crt
    key_file: /etc/greenlens/node-a.key
    client_ca_file: /etc/greenlens/ca.crt
```

Con `client_ca_file` el agente puede escuchar fuera de loopback sin ningún
token de acceso: el certificado del hub es la entrada. Un cliente que lo
muestra lee la API con el rol de lector, así que puede leer mediciones y no
cambia nada. Los clientes sin certificado, como un navegador, siguen
necesitando un token o una sesión.

La unidad de systemd niega toda dirección salvo loopback. Copia el drop-in
`deploy/systemd/greenlens-network.conf` y anota en él la dirección del hub.

## Un certificado para el hub

```bash
greenlens hub issue-cert --ca-dir /etc/greenlens/fleet-ca --out ./certs \
  --name hub --role hub
```

Los dos roles no son intercambiables. El certificado de un agente solo
identifica a un servidor y el del hub solo identifica a un cliente, así que
un nodo comprometido no puede usar su propio certificado para leer a los
demás.

## Leer la flota con Prometheus

Un Prometheus que tiene el certificado del hub consulta a todos los
agentes, y el [tablero de Grafana](../prometheus-grafana/) los muestra
juntos: su variable Node elige un nodo o los suma.

```yaml
scrape_configs:
  - job_name: greenlens
    scheme: https
    tls_config:
      ca_file: /etc/prometheus/greenlens/ca.crt
      cert_file: /etc/prometheus/greenlens/hub.crt
      key_file: /etc/prometheus/greenlens/hub.key
    static_configs:
      - targets: ["10.0.0.11:10060", "10.0.0.12:10060", "10.0.0.13:10060"]
```

Los mismos tres archivos sirven con `curl` para revisar un agente a mano:

```bash
curl --cacert ca.crt --cert hub.crt --key hub.key https://10.0.0.11:10060/api/v1/status
```

Un [collector de OpenTelemetry](../opentelemetry/) es el otro camino para
reunir una flota: cada agente le envía sus métricas, y el collector no
necesita ningún certificado de la flota.

## Renovar los certificados

Los certificados duran 30 días, salvo que `--days` diga otra cosa, hasta
366. Una vida corta limita lo que vale una llave copiada, y no hay lista de
revocación que mantener: un certificado que debe dejar de servir
simplemente no se renueva.

Para renovar, emite el mismo nombre otra vez y copia los dos archivos sobre
los anteriores:

```bash
greenlens hub issue-cert --ca-dir /etc/greenlens/fleet-ca --out ./certs \
  --name node-a --host 10.0.0.11 --host node-a.example.org
```

El agente nota los archivos nuevos en segundos y sirve el certificado nuevo
sin reiniciar; un par que todavía no coincide, mientras un archivo se copia
antes que el otro, se intenta de nuevo. Tras reemplazar el certificado del
hub, recarga Prometheus para que lea los archivos nuevos. Renueva a dos
tercios de la vida, por ejemplo cada 20 días con una tarea programada en la
máquina que guarda la autoridad.

La fecha en que expira un certificado está en la salida de `issue-cert`,
también como JSON con `--output json`, y en el certificado mismo:

```bash
openssl x509 -in node-a.crt -noout -enddate
```

## Reemplazar la autoridad

Cuando la autoridad se acerque a su fin, crea una nueva en otro directorio,
emite certificados nuevos con ella y cambia los nodos uno por uno. Durante
el cambio un agente confía en una sola autoridad, así que cambia
`client_ca_file`, el certificado y la llave de un nodo junto con los
archivos que el hub usa para él.
