---
title: "Intensidad de carbono del software"
description: "Cómo GreenLens calcula el SCI de ISO/IEC 21031 del nodo, de cada backend, modelo y consumidor, con casos resueltos."
weight: 42
---

La intensidad de carbono del software (SCI) de la Green Software
Foundation, publicada como ISO/IEC 21031:2024, es el carbono del software
por unidad funcional:

```text
SCI = (O + M) / R
O   = E x I, las emisiones operacionales
M   = carbono incorporado x (tiempo / vida útil) x parte del hardware
R   = la unidad funcional
```

## O, las emisiones operacionales

E es la electricidad del procesador, la memoria y la GPU, con la sobrecarga
de la instalación (`indicators.pue`). I es la intensidad de la red: el
factor de emisión o, con un perfil horario, la intensidad de cada hora. O
son por lo tanto las emisiones operacionales de los demás reportes.

## M, el carbono incorporado

M es el carbono de fabricar el hardware, repartido en su vida. Define las
cifras de las huellas de carbono de producto que los fabricantes publican
para sus servidores y GPU:

```yaml
indicators:
  sci:
    functional_unit: 1k_generated_tokens
    embodied:
      server_kgco2e: <carbono incorporado del servidor>
      gpu_kgco2e: <carbono incorporado de las GPU>
      lifetime_years: <años de uso>
```

No hay valores predeterminados. Sin ellos, el SCI es solo O / R y cada
reporte dice qué valores faltan. Un año de vida útil son 365.25 días.

El nodo carga con todo el hardware. Un backend, modelo o consumidor carga
con la parte del servidor que su energía de procesador y memoria representa
de la del nodo, y con la parte de la GPU que representa su energía de GPU.
La energía que ningún backend explica se queda con el nodo.

## R, la unidad funcional

`1k_generated_tokens`, la predeterminada, cuenta miles de tokens generados;
`request` cuenta solicitudes y `consumer` los consumidores que el proxy
distingue. `greenlens sci --unit` y el parámetro `unit` de la API eligen
otra para un solo reporte.

## Casos resueltos

Son las cifras que verifican las pruebas de GreenLens. Supón un día de 2.4
kWh de componentes, 1.5 de procesador y memoria y 0.9 de GPU, un PUE de
1.2, el factor de 444 gCO2e/kWh, un servidor de 1,200 kgCO2e y GPU de 300
kgCO2e en 4 años, y 50,000 tokens generados.

**El nodo.** O = 2.4 x 1.2 x 444 = 1,278.72 g. El día es 24 / (4 x 8,766)
de la vida útil, así que M = (1,200,000 + 300,000) x 24 / 35,064 = 1,026.69
g. R = 50, y SCI = (1,278.72 + 1,026.69) / 50 = 46.11 gCO2e por cada 1,000
tokens.

**Un modelo.** Usó 1.71 kWh, 0.9 de procesador y memoria y 0.81 de GPU, y
generó 40,000 tokens. Sus partes son 0.9 / 1.5 = 0.6 del servidor y 0.81 /
0.9 = 0.9 de la GPU. O = 1.71 x 1.2 x 444 = 911.09 g, M = (1,200,000 x 0.6 +
300,000 x 0.9) x 24 / 35,064 = 677.62 g, y SCI = (911.09 + 677.62) / 40 =
39.72 gCO2e por cada 1,000 tokens.

**Sin carbono incorporado.** El nodo da 1,278.72 / 50 = 25.57 gCO2e por cada
1,000 tokens, reportado sin M.

## Reportes y métricas

Los reportes y `greenlens sci` toman las partes de todo el periodo que
cubren. La métrica `greenlens_sci_grams_co2e_per_unit` y el ámbito
`embodied` de `greenlens_emissions_grams_co2e_total` suman M intervalo por
intervalo desde que inició el agente, con las partes de cada intervalo, así
que el contador nunca disminuye.
