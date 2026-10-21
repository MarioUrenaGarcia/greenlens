---
title: "Respaldar, restaurar y exportar datos"
description: "Guarda una copia de la base de datos, llévala a otra máquina y entrega mediciones a otras herramientas."
weight: 55
---

El agente guarda todas las mediciones en una sola base de datos SQLite,
`greenlens.db`, en su directorio de datos (`agent.data_dir`,
`/var/lib/greenlens` para el servicio del sistema).

## Respaldar

```bash
greenlens backup greenlens-2026-10-20.db
```

Si el agente está en ejecución, él mismo hace la copia con la interfaz de
respaldo de SQLite, así que las mediciones continúan y no necesitas acceso a
su directorio de datos, solo a su socket. La copia refleja un solo momento, se
comprueba antes de recibir su nombre, solo tú puedes leerla y nunca reemplaza
un archivo existente. Sin un agente en ejecución se copia la base de datos de
tu usuario; `--database` indica otra:

```bash
sudo -u greenlens greenlens backup --database /var/lib/greenlens/greenlens.db /tmp/greenlens.db
```

## Restaurar

El agente debe estar detenido, porque mantiene abierta la base de datos:

```bash
sudo systemctl stop greenlens
sudo -u greenlens greenlens restore --database /var/lib/greenlens/greenlens.db /tmp/greenlens.db
sudo systemctl start greenlens
```

`greenlens restore` primero comprueba que el archivo sea una base de datos
completa de GreenLens que esta versión pueda leer; si no lo es, no cambia
nada. Después copia la base de datos actual junto a ella como
`greenlens.db.before-restore-<hora>`, salvo con `--no-keep`, y el respaldo la
reemplaza. Un respaldo de una versión anterior se actualiza al esquema actual;
uno de una versión más reciente se rechaza con GL4010.

## Exportar

`greenlens export` escribe CSV por defecto, o JSON y YAML con `--output`. Los
nombres de columnas y campos son iguales en todos los idiomas y los valores
están en unidades base, joules y tokens, así que los scripts no dependen del
idioma del usuario.

```bash
greenlens export energy --from 7d --bucket 1h > energia.csv
greenlens export usage --from 30d --group consumer > consumidores.csv
greenlens export usage --from 7d --bucket 24h --group model --output json
greenlens export benchmarks --output json > benchmarks.json
```

| Datos | Filas |
|---|---|
| `energy` | energía de cada canal por intervalo, con su fuente, dominio, calidad y número de muestras |
| `usage` | energía, tokens y solicitudes de cada backend, modelo o consumidor, del periodo o por intervalo |
| `benchmarks` | cada benchmark guardado con su energía por token generado y sus estadísticas; la salida JSON conserva cada corrida |

Las horas del CSV están en UTC; las de JSON y YAML llevan su desfase. Las
exportaciones leen la resolución más fina que se conserva para el inicio del
periodo: muestras crudas de los últimos 7 días, agregados de un minuto por 90
días y de una hora por 2 años con la configuración predeterminada.
