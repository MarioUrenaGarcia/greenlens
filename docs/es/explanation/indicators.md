---
title: "Emisiones, costo y agua"
description: "Cómo la energía se vuelve kWh, gCO2e, costo y litros, y de dónde vienen los factores."
weight: 40
---

```text
kWh   = joules / 3,600,000
gCO2e = kWh x factor de emisión x PUE
costo = kWh x tarifa x PUE
agua  = kWh x WUE
```

## Factor de emisión

Por defecto GreenLens usa el factor de emisión del Sistema Eléctrico Nacional
(FESEN) de 2024: 0.444 tCO2e/MWh, es decir 444 gCO2e/kWh, publicado por
SEMARNAT el 2025-02-28 para el Registro Nacional de Emisiones. Un aviso de
SEMARNAT del 2026-05-29 lo mantiene vigente mientras se estima el factor de
2025. En otros lugares, define el factor de tu red y su fuente:

```yaml
indicators:
  emission_factor_g_per_kwh: 233
  emission_factor_source: "Operador de la red, factor anual 2025"
```

## PUE, tarifa y agua

`indicators.pue` considera la sobrecarga de la instalación, como el
enfriamiento; 1.0 significa ninguna. No hay tarifa por defecto, porque el
precio de la electricidad varía por tarifa, región, mes y hora; define
`indicators.tariff.price_per_kwh` con tu recibo para ver costos. El agua solo
se reporta cuando se define `indicators.wue_l_per_kwh`.
