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

## Correr el hub

`greenlens hub` lee a los agentes listados en su configuración cada
`interval` y los muestra juntos en una dirección de loopback:

```yaml
hub:
  listen: 127.0.0.1:10061
  interval: 15s
  tls:
    cert_file: /etc/greenlens/hub.crt
    key_file: /etc/greenlens/hub.key
    ca_file: /etc/greenlens/ca.crt
  nodes:
    - name: node-a
      endpoint: https://10.0.0.11:10060
    - name: node-b
      endpoint: https://10.0.0.12:10060
```

```bash
greenlens hub --config /etc/greenlens/hub.yaml
```

El nombre de un nodo debe ser el nombre de su certificado. El hub rechaza a
un agente cuyo certificado no firmó la autoridad, a uno que responde con el
certificado de otro nodo y a uno cuyo certificado no nombra la dirección de
`endpoint`.

| Ruta | Responde | Acceso |
|---|---|---|
| `/metrics` | todas las series de cada nodo que responde, con una etiqueta `node`, y el estado de cada nodo como `greenlens_hub_node_up` | abierto en loopback |
| `/api/v1/fleet` | cada nodo con su estado, versión, potencia y número de modelos, y la potencia de la flota | token de acceso |
| `/api/v1/fleet/energy` | la energía de cada nodo en un periodo, hora por hora y dominio por dominio; `from` y `to` en RFC 3339, las últimas 24 horas sin ellos | token de acceso |
| `/healthz` | si el hub está en ejecución | abierto |

El hub usa los tokens de acceso de la máquina en la que corre, con
cualquier rol: solo lee.

```bash
greenlens token create --config /etc/greenlens/hub.yaml --name fleet-panel --role viewer
curl -H "Authorization: Bearer $GREENLENS_TOKEN" http://127.0.0.1:10061/api/v1/fleet
```

```json
{
  "schema_version": 1,
  "time": "2026-11-15T21:50:00Z",
  "nodes_up": 1,
  "power_w": 70.8,
  "nodes": [
    {"name": "node-a", "endpoint": "https://10.0.0.11:10060", "up": true,
     "last_seen": "2026-11-15T21:49:55Z", "version": "v1.0.0", "power_w": 70.8,
     "power_by_domain": {"core": 9.2, "gpu": 15.8, "package": 55.0}, "models": 2},
    {"name": "node-b", "endpoint": "https://10.0.0.12:10060", "up": false,
     "error": "dial tcp 10.0.0.12:10060: connect: connection refused", "power_w": 0, "models": 0}
  ]
}
```

La potencia y la energía de un nodo suman sus procesadores, memoria y GPU.
Los núcleos son parte del procesador, y una lectura de plataforma o de
pared ya los incluye a todos, así que esos dominios se muestran y nunca se
suman.

El hub guarda la energía de cada nodo en `hub.db`, en su directorio de
datos, tanto tiempo como `retention.rollup_1h`. Mide la energía entre dos
lecturas de los contadores de un agente, así que el tiempo en que un nodo o
el hub estuvieron caídos no se cuenta para ese nodo, y un agente que se
reinicia empieza una cuenta nueva. Un nodo que deja de responder desaparece
de `/metrics`, salvo `greenlens_hub_node_up`, que entonces vale 0.

El hub solo escucha en loopback. Alcánzalo desde otra máquina con un túnel
SSH, o deja que el Prometheus de esa máquina lo consulte.

## Leer la flota con Prometheus

Prometheus puede consultar al hub, cuyas series ya llevan la etiqueta
`node`, o a los agentes directamente. Un Prometheus que tiene el
certificado del hub consulta a todos los agentes, y el [tablero de
Grafana](../prometheus-grafana/) los muestra juntos: su variable Node elige
un nodo o los suma.

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
