---
title: "Exportar métricas con OpenTelemetry"
description: "Envía las métricas del agente a un collector de OpenTelemetry con OTLP, por gRPC o HTTP."
weight: 56
---

El agente responde las consultas de Prometheus en `/metrics`. También puede
enviar las mismas métricas a un collector de OpenTelemetry con OTLP, lo que
conviene donde la canalización empieza en un collector. La exportación está
apagada por defecto, como toda conexión saliente de GreenLens.

## Activar la exportación

```yaml
export:
  otlp:
    enabled: true
    endpoint: http://127.0.0.1:4317
    protocol: grpc
    interval: 15s
```

| Clave | Significado |
|---|---|
| `endpoint` | URL del collector. Debe resolver a una dirección de loopback, privada o link-local; el agente revisa la dirección en cada conexión y rechaza cualquier otra. |
| `protocol` | `grpc`, en el puerto 4317 por convención, o `http`, en el 4318, con cuerpos protobuf. Con `http`, un endpoint sin ruta recibe `/v1/metrics`. |
| `interval` | Tiempo entre dos exportaciones, de 1 segundo a 1 hora. |
| `token_file` | Archivo con un token que se envía como `Authorization: Bearer`, para los collectors que lo piden. |
| `ca_file` | Archivo PEM con la autoridad que firma el certificado de un collector `https`. Vacío usa las autoridades del sistema. |

Un endpoint `http` se envía sin TLS, lo que conviene con un collector en la
misma máquina. Usa `https` con un collector en otra máquina. `endpoint`,
`protocol`, `interval` y `enabled` aplican al recargar con `SIGHUP`; los dos
archivos se leen al arrancar el agente, así que una ruta nueva, o el primer
endpoint `https`, aplican tras reiniciar.

La unidad de systemd solo permite conexiones de loopback. Para un collector
en otra máquina, copia el drop-in `deploy/systemd/greenlens-network.conf` y
anota en él la red del collector.

## Qué se envía

Cada exportación lleva todas las series de `/metrics`, con los mismos
nombres y etiquetas, así que un tablero o una regla sirven con cualquiera de
los dos caminos.

| Tipo de Prometheus | Dato de OTLP | Notas |
|---|---|---|
| counter | Sum, acumulado y monótono | El tiempo de inicio es el arranque del agente. |
| gauge | Gauge | |
| histogram | Histogram, acumulado | Cada intervalo lleva sus propias observaciones. |

Cada métrica declara su unidad cuando su nombre la dice: `J`, `W`, `s`, `g`,
`L`, `J/{token}`. El recurso lleva `service.name` (`greenlens`),
`service.version` y `host.name`.

Un collector que vuelve a exponer las métricas a Prometheus conserva los
nombres: su exportador no repite una unidad ni un `_total` que el nombre ya
tiene.

## Comprobar que funciona

La exportación se cuenta entre los colectores del agente:

```console
$ curl -s http://127.0.0.1:10060/metrics | grep otlp
greenlens_collector_errors_total{collector="otlp"} 0
greenlens_collector_duration_seconds_count{collector="otlp"} 12
```

Una exportación fallida se registra en el log, a lo más una vez cada cinco
minutos, con lo que respondió el collector. La siguiente exportación envía
los totales actuales, así que un collector que estuvo caído no pierde
energía, solo los puntos intermedios.

## Un collector mínimo

Esta configuración de collector recibe los dos protocolos en loopback e
imprime lo que llega:

```yaml
receivers:
  otlp:
    protocols:
      grpc:
        endpoint: 127.0.0.1:4317
      http:
        endpoint: 127.0.0.1:4318
exporters:
  debug:
    verbosity: normal
service:
  pipelines:
    metrics:
      receivers: [otlp]
      exporters: [debug]
```
