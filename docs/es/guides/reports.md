---
title: "Generar los reportes ambiental y SCI"
description: "Escribe el reporte ambiental y la intensidad de carbono del software de un periodo, como páginas para imprimir o archivos para guardar."
weight: 52
---

GreenLens escribe dos reportes de cualquier periodo a partir de lo que el
agente guardó:

- **Reporte ambiental**: la electricidad del nodo por dominio, backend y
  modelo, sus emisiones de alcance 2 por el método basado en ubicación del
  GHG Protocol, su costo y su agua cuando están configurados, la parte
  medida de la energía, la energía que ningún backend explica y el SCI del
  nodo. El método basado en mercado necesita los factores de emisión del
  proveedor de electricidad y no se reporta.
- **Reporte SCI**: la intensidad de carbono del software del nodo y de cada
  backend, modelo o consumidor, con sus partes operacional e incorporada y
  sus unidades funcionales. Consulta
  [Intensidad de carbono del software](../../explanation/sci/).

Ambos siguen los factores horarios del agente: un perfil horario de la red y
precios por horario, cuando están configurados.

## Desde la línea de comandos

```bash
greenlens report --from 2026-10-01 --to 2026-11-01 --esg
greenlens report --from 2026-10-01 --to 2026-11-01 --esg --html octubre.html
greenlens sci --from 30d --group model
greenlens sci --from 30d --group model --unit request --html sci.html
```

`--html` escribe una página que no necesita nada más: ábrela en un
navegador para leerla o imprimirla en A4. `--output json`, `csv` y `yaml`
dan las mismas cifras para otras herramientas. Las páginas siguen el idioma
del comando.

## Desde la interfaz

La pantalla de reportes tiene enlaces a ambos reportes del periodo que
muestra, como páginas imprimibles y como archivos CSV y JSON.

## Desde la API

```bash
curl -H "Authorization: Bearer $GREENLENS_TOKEN" \
  "http://127.0.0.1:10060/api/v1/reports?kind=esg&format=html&lang=es&from=2026-10-01T00:00:00-06:00&to=2026-11-01T00:00:00-06:00"
```

`kind` es `esg` o `sci`; `format` es `json`, `csv` o `html`; `group_by`
divide el SCI por backend, modelo o consumidor.

## Qué calcula el reporte ambiental

Supón un día con 1.2 kWh de paquete de procesador y 0.3 kWh de memoria,
medidos, 0.9 kWh de GPU, estimados, un medidor de plataforma que leyó 3.1
kWh, un PUE de 1.2, el factor de 444 gCO2e/kWh y un precio de 2.5 por kWh:

- Los componentes suman 2.4 kWh; el medidor de plataforma vuelve a medirlos
  y se muestra pero no se suma.
- Con la sobrecarga, la instalación usó 2.4 x 1.2 = 2.88 kWh.
- Las emisiones de alcance 2 son 2.88 x 444 = 1,278.72 gCO2e, y el costo
  2.88 x 2.5 = 7.20.
- La parte medida es 1.5 / 2.4 = 62.5%, porque la GPU fue estimada.
- Con 1.8 kWh atribuidos a los backends, 0.6 kWh es energía que ningún
  backend explica.

Son las cifras que verifican las pruebas de GreenLens.
