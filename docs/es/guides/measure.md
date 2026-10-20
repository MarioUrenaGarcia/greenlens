---
title: "Medir un comando, un proceso o un servicio"
description: "Atribuye energía a lo que ejecutas y lee el reporte."
weight: 40
---

`greenlens measure` atribuye a una carga la parte de la energía del nodo que
corresponde a su tiempo de CPU. Funciona con o sin el agente.

## Tres formas de elegir qué medir

```bash
greenlens measure -- ollama run llama3.2 "¿Por qué el cielo es azul?"
```

```bash
greenlens measure --pid 4242 --duration 5m
```

```bash
greenlens measure --unit ollama.service --duration 10m
```

Un comando se mide hasta que termina, junto con cada hijo que inicia; su
código de salida pasa a ser el de `measure`, y el reporte va a la salida de
error para que la salida del comando quede limpia. Un PID se mide con sus
hijos hasta que termina o pasa `--duration`. Una unidad de systemd se mide a
través de su cgroup, que cuenta todos los procesos del servicio.

## Cómo leer el reporte

| Línea | Significado |
|---|---|
| Energía atribuida | Energía de CPU y memoria atribuida a la carga |
| Potencia promedio | Energía atribuida dividida entre la duración |
| Proporción de CPU | Tiempo de CPU de la carga sobre el tiempo ocupado del equipo |
| Energía del nodo | Toda la energía de CPU, memoria y GPU medida mientras tanto |
| Emisiones | Energía atribuida por el factor de emisión y el PUE |
| Método y calidad | Cómo se obtuvo el valor: medido, de plataforma o estimado |
| Línea base de reposo | Potencia en reposo que resta la política dynamic |

Sin calibración, la política dynamic no tiene línea base y atribuye toda la
energía por proporción de CPU. Ejecuta primero `greenlens calibrate` para un
reparto justo.

## Para scripts

```bash
greenlens measure --report run.json -o json -- ./benchmark.sh
```

El documento JSON tiene un `schema_version` y sus campos no cambian dentro de
una versión.
