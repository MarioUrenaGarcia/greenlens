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
