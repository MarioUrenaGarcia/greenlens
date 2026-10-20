---
title: "Cómo se atribuye la energía"
description: "De la energía del nodo a la energía de un proceso, y las políticas de reposo."
weight: 30
---

El hardware mide el nodo, no los procesos. GreenLens atribuye a cada carga la
parte de la energía que corresponde a su proporción de tiempo de CPU.

## La fórmula

En cada intervalo de muestreo:

```text
P_total    = energía del paquete / intervalo
P_dinámica = max(0, P_total - P_reposo)
share_b    = tiempo de CPU de la carga / tiempo de CPU ocupado del equipo
E_b        = P_dinámica x intervalo x share_b
```

La energía de memoria se atribuye con la misma proporción. La suma de lo
atribuido nunca supera lo medido; las pruebas verifican esta propiedad en
decenas de miles de casos aleatorios.

## Backends, GPU y modelos

Cada backend de inferencia es un grupo de procesos: su servidor y todos sus
descendientes. Su tiempo de CPU da su parte de la energía del procesador y
de la memoria, como arriba.

La energía de una GPU va a los backends que corren en ella:

- cuando el driver reporta el uso de cada proceso, según ese uso
  (`attributed_gpu_process`): NVML lo reporta para tarjetas NVIDIA, y las
  estadísticas DRM de `/proc/<pid>/fdinfo` para amdgpu, i915 y xe. El kernel
  muestra esas estadísticas solo al dueño del proceso y a lectores con
  privilegios, así que un agente que corre como otro usuario pasa a la
  siguiente regla;
- cuando solo reporta qué procesos tienen un contexto, toda al único backend
  que hay, o en partes iguales entre varios (`estimated`).

Con la política `dynamic` solo se atribuye la energía sobre la potencia en
reposo de cada GPU, medida por `greenlens calibrate`.

Cada diez segundos la energía de un backend se reparte entre sus modelos
según los tokens que procesó cada uno, de prompt y generados
(`token_weighted`). Un único modelo activo se la queda toda. Los modelos más
allá de `attribution.max_models_per_backend` se reportan juntos como
`other`.

## Tokens de prompt y generados

Procesar un prompt y generar tokens no cuestan lo mismo. Cada diez minutos
el agente ajusta, para cada modelo, sobre los intervalos de las últimas 24
horas en que corrió solo:

```text
energía = a x tokens de prompt + b x tokens generados + c
```

por mínimos cuadrados, con intervalos de confianza al 95% de la t de
Student. `a` y `b` son la energía de un token de prompt y de un token
generado; `c` es la energía del modelo que los tokens no explican. Se
reportan, con método `regression`, solo a partir de 30 intervalos y cuando
R2 alcanza `attribution.regression_min_r2` (0.7 por defecto), y nunca cuando
un token costaría energía negativa. Se excluyen los intervalos cuya energía
ya se repartió por tokens, porque solo devolverían ese reparto.

## Políticas de reposo

`attribution.idle_policy` decide qué pasa con la energía de reposo:

| Política | Atribuye | Conviene para |
|---|---|---|
| `dynamic` (por defecto) | solo la energía por encima de la línea base de reposo | equipos compartidos |
| `proportional` | también la energía de reposo, por proporción de CPU | reportes de costo que incluyen el reposo |
| `full` | toda la energía a las cargas activas | nodos dedicados a inferencia |
| `wall` | la energía de plataforma o de pared por encima de su reposo, por proporción | instalaciones que reportan en el enchufe |

## La línea base de reposo

`greenlens calibrate` mide el equipo en reposo durante un minuto y guarda la
mediana de cada dominio, junto con la frecuencia de la CPU, el governor y la
temperatura. Mientras corre, el agente mantiene el percentil 5 de la potencia
de las últimas 24 horas como estimación actual del reposo, y avisa cuando se
aleja más de 20% de la calibración, lo que ocurre tras cambios de hardware o
de firmware.
