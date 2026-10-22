---
title: "Alertas"
description: "Reglas que vigilan la potencia, la energía por token y las fuentes, y adónde van sus notificaciones."
weight: 53
---

Una regla de alerta compara una señal del agente con un umbral cada 15
segundos. Mientras la comparación se sostiene, la alerta está pendiente;
cuando se sostuvo el tiempo de la regla, se dispara; cuando deja de
sostenerse, se resuelve. Las alertas pendientes y disparadas sobreviven a un
reinicio del agente, y cada alerta que se disparó queda en el historial
tanto tiempo como los agregados por hora.

## Señales

| Señal | Unidad | Etiquetas | Valor |
|---|---|---|---|
| `node_power_watts` | W | ninguna | Potencia del paquete de procesador, la memoria y la GPU en la última muestra |
| `backend_power_watts` | W | `backend` | Potencia de cada backend en el último intervalo de uso |
| `model_joules_per_token` | J | `backend`, `model` | Energía por token generado de cada modelo en los últimos 15 minutos, cuando generó al menos 100 tokens |
| `model_jpt_zscore` | ninguna | `backend`, `model` | Z-score de la energía por token de hoy frente a los días anteriores, como lo juzgan las [regresiones](../../explanation/regressions/) |
| `source_up` | 1 o 0 | `source` | Si cada fuente de energía está seleccionada y devolvió valores en la última muestra |

Una regla juzga cada serie de su señal, salvo que `match` deje solo las que
tienen ciertas etiquetas.

## Reglas

```yaml
alerts:
  rules:
    - name: high_joules_per_token
      expr: model_joules_per_token
      op: ">"
      threshold: 5.0
      for: 10m
    - name: efficiency_regression
      expr: model_jpt_zscore
      op: ">"
      threshold: 3.0
      for: 30m
    - name: rapl_down
      expr: source_up
      op: "<"
      threshold: 1
      for: 1m
      match:
        source: rapl
```

`op` es `>`, `>=`, `<` o `<=`; ponlo entre comillas en YAML. `for` es cuánto
debe sostenerse la comparación, como `30s`, `10m` o `1h`; sin él la alerta se
dispara de inmediato. Los nombres son únicos. Los umbrales de arriba son un
punto de partida: elige los que correspondan a tus modelos y a tu hardware.

Recarga el agente para aplicar las reglas, o edítalas en la pantalla
**Alertas** de la [interfaz gráfica](../gui/) con el rol operator. La pantalla
muestra el archivo de configuración que producen las reglas, lo escribe
cuando el agente puede escribir su propio archivo y, si no, entrega el
archivo para instalarlo. Solo cambia `alerts.rules`; los comentarios y las
demás claves del archivo se conservan.

## Notificaciones

Las notificaciones se envían cuando una alerta se dispara y cuando se
resuelve, no mientras está pendiente.

```yaml
alerts:
  sinks:
    log: true
    journald: false
    syslog: false
    webhook:
      enabled: false
      url: ""
      token_file: ""
```

- `log` las escribe en el log del agente; es el valor por defecto.
- `journald` las envía al journal con los campos `GREENLENS_ALERT_RULE`,
  `GREENLENS_ALERT_STATE` y `GREENLENS_ALERT_VALUE`:
  `journalctl SYSLOG_IDENTIFIER=greenlens GREENLENS_ALERT_STATE=firing`.
- `syslog` las envía al daemon de syslog local por `/dev/log`, con la
  facilidad `daemon`.
- `webhook` las envía como JSON a una URL que resuelva a una dirección de
  loopback, privada o link-local; cualquier otra dirección se rechaza al
  conectar, así que un nombre no puede apuntarla hacia fuera.

Activar `journald` o `syslog`, o cambiar `webhook.token_file`, aplica tras
reiniciar, porque el agente limita su propio acceso a archivos al arrancar.

## El webhook

El archivo del token tiene una línea. El agente la envía como
`Authorization: Bearer <token>` y firma cada cuerpo con HMAC-SHA256 usándola
como llave: `X-GreenLens-Timestamp` lleva la hora Unix de la solicitud y
`X-GreenLens-Signature` lleva `sha256=` y el HMAC en hexadecimal de la hora,
un punto y el cuerpo. Un receptor lo comprueba con:

```bash
printf '%s.%s' "$timestamp" "$body" | openssl dgst -sha256 -hmac "$token"
```

y rechaza las solicitudes cuya hora sea demasiado antigua. El cuerpo es la
alerta:

```json
{
  "schema_version": 1,
  "agent_version": "1.0.0",
  "id": 12,
  "rule": "high_joules_per_token",
  "expr": "model_joules_per_token",
  "op": ">",
  "threshold": 5,
  "for_s": 600,
  "labels": {"backend": "ollama-11434", "model": "qwen2.5:7b"},
  "state": "firing",
  "value": 6.21,
  "started_at": "2026-10-21T15:00:00Z",
  "fired_at": "2026-10-21T15:10:00Z"
}
```

Una entrega que falla se intenta tres veces más, con 5 segundos, 30 segundos
y un minuto de espera.

## Desde la línea de comandos

`greenlens status` lista las alertas disparadas o pendientes.
`greenlens export alerts --from 30d` las escribe junto con el historial del
periodo como CSV, o como JSON con `--output json`. `greenlens top` las
muestra en su quinta vista.

## Con Prometheus y Alertmanager

`deploy/prometheus/rules.yml` tiene reglas para Prometheus con las mismas
señales, para quien enruta las alertas con Alertmanager. Para ellas el
agente exporta `greenlens_model_jpt_zscore` y `greenlens_alerts_firing`, las
alertas disparadas de cada regla.
