---
title: "Emisiones, costo y agua"
description: "Cómo la energía se convierte en kWh, gCO2e, costo y litros, hora por hora, y de dónde vienen los factores."
weight: 40
---

```text
kWh   = joules / 3,600,000
gCO2e = kWh x factor de emisión x PUE
costo = kWh x tarifa x PUE
agua  = kWh x WUE
```

El agente calcula estos indicadores por sí mismo, hora por hora, y los
entrega a cada cliente junto con la energía: la línea de comandos, la vista
de terminal, la interfaz, los reportes y las métricas muestran los mismos
valores, los de la configuración del agente.

## Factor de emisión

De forma predeterminada GreenLens usa el factor de emisión del Sistema
Eléctrico Nacional (FESEN) de 2024: 0.444 tCO2e/MWh, es decir, 444
gCO2e/kWh, publicado por la SEMARNAT el 2025-02-28 para el Registro Nacional
de Emisiones. Un aviso de la SEMARNAT del 2026-05-29 lo mantiene vigente
mientras se estima el factor de 2025. En otros lugares, define el factor de
tu red y su fuente:

```yaml
indicators:
  emission_factor_g_per_kwh: 233
  emission_factor_source: "Operador de la red, factor anual 2025"
```

## Un perfil horario de la red

La intensidad de una red cambia con la hora y el día de la semana. Con
`indicators.hourly_profile_file`, la energía de cada hora toma la
intensidad de esa hora de la semana en lugar del factor único. El archivo
es un CSV con esta primera línea y una línea por cada una de las 168 horas
de la semana, con el día ISO (1 es lunes, 7 es domingo) y la hora local de
0 a 23:

```text
weekday,hour,g_co2e_per_kwh
1,0,<intensidad del lunes de 00:00 a 01:00>
1,1,<intensidad del lunes de 01:00 a 02:00>
...
7,23,<intensidad del domingo de 23:00 a 24:00>
```

GreenLens no incluye ningún perfil: usa las cifras del operador de tu red.
Las horas son las de `indicators.timezone`, un nombre IANA como
`America/Mexico_City`, o la zona horaria del sistema cuando está vacío.

Por ejemplo, supón un perfil de 400 gCO2e/kWh los lunes a las 10:00 y de 500
a las 11:00, y un PUE de 1.2. Dos kWh usados de forma pareja de 10:00 a
12:00 ponen un kWh en cada hora: (1 x 400 + 1 x 500) x 1.2 = 1,080 gCO2e.
De 10:30 a 11:30, cae medio kWh en cada hora: (0.5 x 400 + 0.5 x 500) x 1.2
= 540 gCO2e.

## PUE, tarifa y agua

`indicators.pue` considera la sobrecarga de la instalación, como el
enfriamiento; 1.0 significa ninguna. No hay tarifa predeterminada, porque
los precios de la electricidad varían por tarifa, región, mes y hora; define
`indicators.tariff.price_per_kwh` a partir de tu recibo para ver los costos.
El agua solo se reporta cuando se define `indicators.wue_l_per_kwh`.
El WUE relaciona el agua del sitio con la energía del equipo de TI, así que
se aplica a la energía de los componentes, sin el PUE.

Por ejemplo, con un PUE de 1.5, un precio de 2.5 por kWh y un WUE de 1.8
L/kWh, 2 kWh de componentes dan 2 x 444 x 1.5 = 1,332 gCO2e, un costo de
2 x 2.5 x 1.5 = 7.50 y 2 x 1.8 = 3.6 L de agua.

## Precios por horario

`indicators.tariff.periods` lista precios para algunos días de la semana
entre dos horas locales del mismo día; `price_per_kwh` se aplica fuera de
ellos, y sin él los tramos deben cubrir toda la semana. Los tramos que se
traslapan se rechazan. Los escalones de consumo mensual y los cargos por kW
de demanda no se modelan.

```yaml
indicators:
  tariff:
    price_per_kwh: <el precio fuera de los tramos>
    currency: MXN
    periods:
      - days: [mon, tue, wed, thu, fri]
        from: "18:00"
        to: "22:00"
        price_per_kwh: <el precio del tramo>
```

Por ejemplo, supón un precio de 1.0 fuera del tramo y de 3.0 de 18:00 a
22:00 entre semana. Cuatro kWh usados de forma pareja un lunes de 17:00 a
21:00 ponen un kWh al precio base y tres en el tramo: 1 x 1.0 + 3 x 3.0 =
10.0. Un sábado el mismo uso cuesta 4 x 1.0 = 4.0. Un tramo que termina a
las 18:20 divide esa hora: un kWh de 18:00 a 19:00 cuesta (20 x 3.0 + 40 x
1.0) / 60 = 1.67.

## Proyecciones

Las comparaciones que proyectan el ahorro de otro modelo no tienen hora de
uso, así que toman el factor de emisión único y el precio fuera de los
tramos.
