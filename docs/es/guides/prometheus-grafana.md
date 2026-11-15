---
title: "Integrar con Prometheus y Grafana"
description: "Consulta el agente con Prometheus, carga las reglas de alerta y muestra el tablero de Grafana."
weight: 54
---

El agente sirve sus métricas en el formato de texto de Prometheus en
`/metrics` de su API HTTP, en `127.0.0.1:10060` por defecto. Los nombres
empiezan con `greenlens_` y usan unidades base: joules, watts, segundos,
gramos y litros.

## Consultar el agente

```yaml
scrape_configs:
  - job_name: greenlens
    static_configs:
      - targets: ["127.0.0.1:10060"]
```

`/metrics` no pide token, salvo que `server.metrics_auth` esté activo. Con
él activo, crea un token para Prometheus y nombra su archivo en la
configuración de consulta:

```bash
greenlens token create --name prometheus --role viewer
```

```yaml
scrape_configs:
  - job_name: greenlens
    authorization:
      credentials_file: /etc/prometheus/greenlens-token
    static_configs:
      - targets: ["127.0.0.1:10060"]
```

Un Prometheus en otra máquina necesita que el agente escuche en una
dirección interna, lo que solo hace con al menos un token de acceso creado;
consulta [la API HTTP](../http-api/).

## Qué consultar

| Pregunta | Consulta |
|---|---|
| Potencia del nodo | `sum(greenlens_node_power_watts{domain=~"package\|dram\|gpu"})` |
| Energía del nodo en un día, en kWh | `sum(increase(greenlens_node_energy_joules_total{domain=~"package\|dram\|gpu"}[1d])) / 3.6e6` |
| Potencia de cada backend | `sum by (backend) (rate(greenlens_backend_energy_joules_total[5m]))` |
| Energía por token generado de cada modelo | `sum by (backend, model) (rate(greenlens_model_energy_joules_total[15m])) / sum by (backend, model) (rate(greenlens_model_tokens_total{type="generated"}[15m]))` |
| Emisiones de cada modelo en un día, en gramos | `sum by (backend, model) (increase(greenlens_emissions_grams_co2e_total{scope="operational"}[1d]))` |

Suma solo los dominios `package`, `dram` y `gpu` para la energía del nodo.
`core` y `uncore` son parte del paquete, y `psys`, `platform` y `wall` son
lecturas de la máquina completa que ya incluyen a los componentes.

Los contadores empiezan en cero al arrancar el agente, como espera
Prometheus; `rate` e `increase` manejan el reinicio.

## Reglas de alerta

`deploy/prometheus/rules.yml` trae reglas para Alertmanager que
corresponden a las señales de las [reglas de alerta](../alerts/) del
agente. Cópialo junto a la configuración de Prometheus y nómbralo ahí:

```yaml
rule_files:
  - greenlens-rules.yml
```

## El tablero de Grafana

`deploy/grafana/greenlens.json` es un tablero para una fuente de datos de
Prometheus. Impórtalo en Grafana desde Dashboards, New, Import, y elige la
fuente de datos. Muestra:

- la potencia del nodo por dominio, su energía y sus emisiones en el
  periodo, las fuentes que no devuelven valores y las alertas disparadas;
- la potencia de cada backend, y cómo se reparte entre procesador, memoria
  y GPU;
- la energía por token generado y los tokens generados por segundo de cada
  modelo, y una tabla con los totales del periodo;
- la potencia del propio agente y los errores y tiempos de lectura de sus
  colectores.

Las variables Node, Backend y Model acotan todos los paneles. Con varios
agentes en un mismo Prometheus, Node elige uno o los suma.

## Un entorno local

`deploy/compose/compose.yaml` levanta Prometheus, Grafana y un collector
de OpenTelemetry ya configurados para el agente de esta máquina, para
probar las integraciones:

```bash
docker compose -f deploy/compose/compose.yaml up
```

Grafana responde en `http://127.0.0.1:3000` con el tablero como página de
inicio, y Prometheus en `http://127.0.0.1:9090`. Todos los servicios
escuchan solo en loopback. Para ver también la [exportación con
OpenTelemetry](../opentelemetry/), actívala con el endpoint
`http://127.0.0.1:4317`; el collector entrega lo que recibe a Prometheus
como el trabajo `greenlens-otlp`.
