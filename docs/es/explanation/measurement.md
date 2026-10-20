---
title: "Cómo se mide la energía"
description: "Las fuentes de energía, cómo se elige una por dominio y qué significa la calidad."
weight: 20
---

GreenLens lee la energía de las interfaces del kernel de Linux y elige, para
cada parte del equipo, una sola fuente, así la misma energía nunca se cuenta
dos veces.

## Dominios

| Dominio | Qué cubre | Cómo se combina |
|---|---|---|
| Paquete de CPU | Un zócalo de procesador | se suma al nodo |
| Núcleos y uncore de CPU | Partes de un paquete | contenidos en el paquete, no se suman |
| Memoria | DRAM de un zócalo | se suma al nodo |
| GPU | Una tarjeta gráfica | se suma al nodo |
| Plataforma (psys), plataforma, pared | Todo el sistema o más | incluyen los componentes, nunca se suman |

La energía del nodo es la suma de los paquetes, la memoria y las GPU. Las
mediciones de plataforma y de pared sirven para ver lo que usa el resto del
sistema.

## Fuentes y prioridad

| Prioridad | Fuente | Dominios | Calidad |
|---|---|---|---|
| 1 | powercap RAPL | paquete, núcleos, uncore, memoria, psys | medido |
| 2 | perf_event `power` | paquete, memoria, psys | medido |
| 3 | drivers de procesador de hwmon | paquete, memoria | medido |
| 3 | drivers de GPU de hwmon: amdgpu, xe, i915 | GPU, o el paquete de una APU de AMD | medido |
| 4 | medidor ACPI, IPMI DCMI, batería | plataforma | plataforma |
| 6 | Modelo estimado | paquete | estimado |

powercap y perf_event leen los mismos contadores de hardware; GreenLens usa
uno. `greenlens doctor` muestra cada canal encontrado, si se usa y por qué no.
`sources.disable` y `sources.force` cambian la elección.

## GPU

Se elige una fuente para cada tarjeta, identificada por su dirección PCI, así
una máquina con tarjetas de distintos fabricantes las mide todas.

- **amdgpu** reporta potencia en watts (`power1_average`, o `power1_input`
  cuando el firmware no tiene promedio). En una APU, un procesador con
  gráficos integrados, el sensor cubre también la CPU, así que GreenLens lo
  reporta como el paquete y lo usa solo cuando no se pueden leer RAPL ni
  perf_event.
- **xe** reporta la energía de la tarjeta, o del paquete de la GPU donde falta
  el contador de la tarjeta.
- **i915** reporta la energía de la tarjeta.

Los drivers de Intel registran estos sensores solo para tarjetas dedicadas,
cuya energía no forma parte del paquete del procesador. Una tarjeta en
suspensión no se lee, porque la lectura la despertaría; su potencia es cero
hasta que se reactiva.

## De las lecturas a la energía

Los contadores reportan energía acumulada. GreenLens resta lecturas
consecutivas y, cuando un contador vuelve a cero, suma la distancia hasta su
máximo (`max_energy_range_uj`). En un procesador de 200 W, un contador con
rango de 65 kJ se desborda cada cinco minutos, mucho más que el intervalo de
muestreo de un segundo; `GL1008` avisa cuando un intervalo es demasiado largo
para la potencia observada.

Los sensores de potencia reportan watts; GreenLens los integra con la regla
del trapecio. Los intervalos se miden con el reloj monotónico, así los
cambios del reloj del sistema no los distorsionan.

## Calidad

Cada valor lleva su calidad:

- **medido**: de un contador de energía o sensor de potencia del componente;
- **plataforma**: de un medidor de todo el sistema, que incluye partes que
  ninguna otra fuente cubre, como ventiladores y discos;
- **estimado**: del modelo, cuando no se puede leer nada.

Ningún número se muestra sin su unidad, su método y su calidad.
