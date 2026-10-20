---
title: "Máquinas virtuales y hardware sin sensores"
description: "Cómo estima GreenLens la energía de la CPU y cómo mejorar la estimación."
weight: 50
---

Las máquinas virtuales normalmente no exponen ningún contador de energía.
Entonces GreenLens estima la potencia de la CPU a partir de su uso:

```text
P = hilos x (Pmin + (Pmax - Pmin) x uso^k)
```

Cada valor se marca como `estimado`, y `greenlens doctor` reporta `GL1007`.

## De dónde salen los coeficientes

Pmin y Pmax son la potencia de un hilo de hardware al 0% y al 100% de uso.
GreenLens incluye los coeficientes por hilo publicados por Cloud Carbon
Footprint, derivados de resultados de SPECpower, para generaciones de Intel
Xeon y AMD EPYC. El procesador se identifica por sus números de familia y
modelo. Si no está en la tabla, GreenLens usa el promedio de la misma fuente,
de 0.74 W a 3.5 W por hilo, y reporta `GL1010`.

El uso excluye el tiempo robado (steal), que el hipervisor da a otros
invitados.

## Mejores valores para tu hardware

Si conoces la potencia de tu CPU en reposo y a plena carga, por ejemplo por el
fabricante o por un medidor de pared, defínela:

```yaml
sources:
  estimate:
    idle_watts: 35
    full_watts: 180
    exponent: 1
```

Los valores de un medidor en el mismo modelo de equipo dan la mejor
estimación.
