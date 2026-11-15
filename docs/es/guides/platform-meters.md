---
title: "Medir la máquina completa: Redfish y medidores de pared"
description: "Lee la potencia del servidor desde su controlador de gestión o desde un enchufe inteligente, y valida con ella las otras fuentes."
weight: 22
---

Los contadores del procesador y de la GPU dejan fuera el resto de la
máquina: la tarjeta madre, los discos, los ventiladores y las pérdidas de
la fuente de poder. Dos fuentes leen la máquina completa por la red. Las
dos están apagadas por defecto, porque hacen que el agente se conecte a
otro dispositivo, y las dos solo alcanzan direcciones de loopback, privadas
o link-local.

| Fuente | Lee | Dominio | Dispositivo |
|---|---|---|---|
| `redfish` | la potencia del chasis, como la reporta su controlador | plataforma | controlador de gestión (BMC) del servidor |
| `wall_meter` | la potencia en el enchufe | pared | enchufe inteligente Shelly o Tasmota |

Una lectura de plataforma o de pared ya incluye al procesador, la memoria y
la GPU, así que GreenLens nunca la suma a ellos. La muestra como un dominio
propio, la usa para saber cuánto consume el resto de la máquina y la
atribuye con la [política de reposo](../../explanation/attribution/) `wall`.

La unidad de systemd solo permite conexiones de loopback: copia el drop-in
`deploy/systemd/greenlens-network.conf` y anota en él la dirección del
controlador o del medidor. Un cambio en estos ajustes aplica al reiniciar
el agente.

## Redfish

```yaml
sources:
  redfish:
    enabled: true
    endpoint: https://10.0.0.5
    token_file: /etc/greenlens/redfish-token
    ca_file: /etc/greenlens/bmc-ca.pem
    interval: 5s
```

El agente lee `PowerWatts` del recurso `EnvironmentMetrics` del chasis, y
`PowerControl.PowerConsumedWatts` del recurso `Power` en los controladores
anteriores a él. Usa el primer chasis que reporta su potencia; `chassis`
nombra otro, por la última parte de su ruta, como `System.Embedded.1`.

`token_file` guarda el token de una sesión de Redfish, que el agente envía
como `X-Auth-Token`. Crea para él una cuenta en el controlador con el rol
`ReadOnly`, abre una sesión y guarda el token que el controlador devuelve
en el encabezado `X-Auth-Token` de su respuesta:

```bash
curl --cacert /etc/greenlens/bmc-ca.pem -D - -o /dev/null \
  -H 'Content-Type: application/json' \
  -d '{"UserName": "greenlens", "Password": "..."}' \
  https://10.0.0.5/redfish/v1/SessionService/Sessions
```

Escribe solo el token en el archivo, legible únicamente por el usuario del
agente. Una sesión termina tras el tiempo de inactividad del controlador,
normalmente 30 minutos; las lecturas cada pocos segundos la mantienen
abierta. También termina cuando el controlador se reinicia: `greenlens
doctor` reporta entonces GL1016 con una respuesta 401, y el archivo necesita
un token nuevo.

Los controladores vienen con un certificado firmado por ellos mismos.
Expórtalo, o exporta la autoridad que firma el que instalaste, y nómbralo
en `ca_file`. El agente siempre verifica el certificado.

## Un medidor de pared

```yaml
sources:
  wall_meter:
    enabled: true
    kind: shelly
    endpoint: http://192.168.1.50
    channel: 0
    interval: 2s
```

| Tipo | Dispositivos | Cómo se lee |
|---|---|---|
| `shelly` | enchufes y relevadores Shelly con medición de potencia, de cualquier generación | `/rpc/Switch.GetStatus` desde la segunda generación, `/meter/{channel}` en la primera |
| `tasmota` | dispositivos con el firmware Tasmota y un sensor de energía | `Status 10` |

`channel` elige la salida de un dispositivo con varias, contada desde cero.
El medidor debe responder sin contraseña, así que mantenlo en una red que
solo alcancen las máquinas que mide.

Conecta al medidor solo la máquina medida. Un medidor que también alimenta
un monitor o un switch reporta su potencia como la de la máquina.

## Validar las otras fuentes

Un medidor de pared es la referencia para revisar los contadores. Con la
máquina en reposo, y luego bajo una carga estable, compara los dominios
durante unos minutos:

```bash
greenlens report --from 10m
```

La energía de pared es mayor que la suma del procesador, la memoria y la
GPU: la diferencia es el resto de la máquina y las pérdidas de la fuente de
poder, típicamente de 10% a 20% de la potencia de pared bajo carga y una
parte mayor en reposo. Lo que valida los contadores es que la diferencia se
mantenga estable mientras la carga cambia: cuando el procesador consume 50 W
más, la pared debe mostrar 50 W más, más las pérdidas sobre esos 50 W.

Los medidores de enchufe reportan cerca de una vez por segundo y redondean
a una décima de watt o más, así que compara energías de minutos, no
muestras sueltas.

## Cuando una fuente deja de responder

El agente lee las dos en segundo plano, así que un dispositivo lento o
caído nunca retrasa a las otras fuentes. Tras tres intervalos sin respuesta
el dominio queda sin valor, en lugar de repetir el último, y las lecturas
se reanudan solas cuando el dispositivo vuelve a responder. La señal
`source_up` de las [alertas](../alerts/) puede vigilarlo:

```yaml
alerts:
  rules:
    - name: wall_meter_down
      expr: source_up
      op: "<"
      threshold: 1
      for: 2m
      match:
        source: wall_meter
```
