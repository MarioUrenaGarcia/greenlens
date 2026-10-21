---
title: "Regresiones de la energía por token"
description: "Cómo GreenLens juzga cada día de un modelo frente a los días anteriores, y por qué muestra los cambios de contexto."
weight: 45
---

La energía por token generado de un modelo se mueve con la carga, la
longitud de los prompts y la temperatura de la máquina. Una nueva versión
del motor, un driver de GPU o un archivo distinto bajo el mismo nombre de
modelo pueden moverla de forma permanente. GreenLens distingue ambos casos
día por día.

## La prueba

Para cada día local en que un modelo generó al menos
`analysis.regression.min_generated_tokens` tokens, GreenLens toma su
energía por token generado, x, y el mismo valor de cada uno de los
`analysis.regression.baseline_days` días del calendario anteriores que
también tuvieron suficientes tokens. Con al menos
`analysis.regression.min_baseline_days` de ellos, calcula su media, m, y su
desviación estándar muestral, s:

```
z = (x - m) / max(s, 0.005 x m)
```

Un día con z de `analysis.regression.z_threshold` o más es una regresión; un
día con z de menos ese umbral o menos es una mejora. El piso de 0.5% de la
media evita que una línea base totalmente estable convierta cualquier
cambio pequeño en una regresión. Los días son los de `indicators.timezone`.

## Un caso resuelto

Siete días de base a 0.20, 0.21, 0.19, 0.20, 0.22, 0.18 y 0.20 J por token
tienen una media de 0.20 J. Sus desviaciones respecto de ella, al cuadrado,
suman 0.0010, así que s = sqrt(0.0010 / 6) = 0.012910 J. Un día a 0.25 J
tiene z = 0.05 / 0.012910 = 3.873: una regresión con el umbral
predeterminado de 3. Un día a 0.21 J tiene z = 0.775 y es normal; uno a
0.15 J tiene z = -3.873 y es una mejora.

## Cambios de contexto

El agente registra cuándo cambia algo de lo que dependen las mediciones: su
propia versión, el kernel, el driver de NVIDIA, la versión de cada backend,
el archivo de cada modelo cuando el motor reporta su digest (Ollama lo hace)
y la configuración. La pantalla de modelos lista estos cambios junto a los
días de un modelo, y `greenlens report --regressions` los imprime después de
los días que destacan, para rastrear una regresión hasta su causa.

Las regresiones se reportan, no se alertan; las notificaciones pertenecen a
las alertas.
