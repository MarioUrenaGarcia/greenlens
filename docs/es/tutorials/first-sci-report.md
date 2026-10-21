---
title: "Tu primer reporte SCI"
description: "Mide un modelo durante un día, agrega el carbono incorporado del hardware e imprime la intensidad de carbono del software."
weight: 20
---

Este tutorial produce la intensidad de carbono del software (SCI) de un
modelo que sirve Ollama, desde sus primeras solicitudes hasta un reporte
impreso. Supone que el agente ya está en ejecución, como en
[Primeros diez minutos](../first-steps/).

## 1. Contar los tokens

El SCI divide las emisiones entre los tokens generados, así que el agente
debe contarlos. Ollama no cuenta tokens por sí mismo; activa el proxy de
GreenLens, reinicia el agente, porque los ajustes del proxy se aplican al
iniciar, y envía tus solicitudes al proxy en lugar de a Ollama:

```yaml
proxy:
  enabled: true
  listen: 127.0.0.1:11435
  upstream: http://127.0.0.1:11434
```

```bash
curl http://127.0.0.1:11435/api/generate -d '{"model": "qwen2.5:0.5b", "prompt": "¿Por qué el cielo es azul?"}'
```

Consulta [Contar tokens con el proxy](../../guides/proxy/) para los detalles.

## 2. Mira la parte operacional

Después de algunas solicitudes, el SCI del nodo y de cada modelo ya está
ahí, sin carbono incorporado:

```bash
greenlens sci --from 1h --group model
```

El reporte dice que no incluye el carbono incorporado y qué claves le
faltan.

## 3. Agrega el carbono incorporado

Busca la huella de carbono de producto que publica el fabricante de tu
servidor y de tu GPU, y cuántos años planeas usarlos. Luego defínelos en la
configuración del agente y recárgala:

```yaml
indicators:
  sci:
    embodied:
      server_kgco2e: <carbono incorporado del servidor>
      gpu_kgco2e: <carbono incorporado de la GPU>
      lifetime_years: <años de uso>
```

```bash
sudo systemctl reload greenlens
```

## 4. Imprime el reporte

Después de un día de uso:

```bash
greenlens sci --from 24h --group model --html sci.html
```

Abre `sci.html` en un navegador e imprímelo. Muestra O, M, R y el SCI del
nodo y de cada modelo, con el método. El mismo reporte está enlazado desde
la pantalla de reportes de la interfaz, y
[Generar los reportes ambiental y SCI](../../guides/reports/) describe el
reporte ambiental junto a él.
