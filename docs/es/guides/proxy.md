---
title: "Contar tokens con el proxy"
description: "Mide la energía por token y por consumidor de Ollama y de otros servidores que no cuentan tokens."
weight: 47
---

Ollama y muchos servidores compatibles con OpenAI no publican contadores de
tokens. El agente puede ejecutar delante de ellos un proxy inverso que lee
los conteos de tokens de cada respuesta a su paso.

## Qué lee el proxy

El proxy reenvía cada petición y cada respuesta sin cambios, incluidas las
transmitidas por partes. De las respuestas solo lee el nombre del modelo y
los conteos de tokens: `prompt_eval_count` y `eval_count` de Ollama, `usage`
de los servidores compatibles con OpenAI, o el número de partes transmitidas
cuando una transmisión no trae un conteo final. Nunca lee el cuerpo de las
peticiones, y nunca guarda ni registra prompts, respuestas ni claves; las
pruebas verifican que ninguno llegue a los logs ni al directorio de datos.

## Activarlo

```yaml
proxy:
  enabled: true
  listen: 127.0.0.1:11435
  upstream: http://127.0.0.1:11434
```

Recarga el agente y apunta tus clientes a `http://127.0.0.1:11435` en lugar
del servidor. `greenlens proxy` muestra su estado. Los tokens que cuenta el
proxy se usan para los backends que no los cuentan; para vLLM, TGI, SGLang y
llama.cpp se conservan sus propios contadores, así ningún token se cuenta
dos veces.

## Energía por petición

Con `per_request_energy_header: true`, cada respuesta lleva
`X-GreenLens-Energy-Joules`: la energía del backend mientras corrió la
petición, repartida entre las peticiones en curso según la salida que
transmitió cada una, o en partes iguales mientras se procesan los prompts.
Las respuestas transmitidas por partes la llevan como trailer HTTP, porque
solo se conoce al final.

## Consumidores

`consumer_header` nombra la cabecera de la petición que distingue a los
consumidores, como `Authorization` o `X-Api-Key`. Su valor nunca se guarda:
un consumidor se registra por el HMAC del valor con una sal guardada en el
directorio de datos, y se nombra `consumer-` seguido del inicio de ese HMAC.
Para dar nombre a los consumidores, lista el SHA-256 del valor de su cabecera
en `consumer_aliases_file`:

```bash
printf '%s' 'Bearer sk-example' | sha256sum
```

```yaml
team-a: sha256:<los 64 dígitos hexadecimales que imprimió el comando>
```

Después, `greenlens report --group consumer` muestra la energía de cada uno.
