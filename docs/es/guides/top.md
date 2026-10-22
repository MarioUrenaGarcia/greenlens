---
title: "Observar el nodo en vivo"
description: "Sigue en la terminal la potencia del nodo, de sus backends y de sus fuentes."
weight: 35
---

`greenlens top` muestra lo que mide el agente, actualizado cada segundo.

```bash
greenlens top
greenlens top --refresh 5s
```

| Vista | Tecla | Muestra |
|---|---|---|
| Nodo | `1` | la potencia de cada dominio en los últimos minutos y la energía desde que inició el agente |
| Backends | `2` | cada backend y modelo: potencia, energía, energía por token generado, tokens por segundo, CO2e y, con una tarifa, costo |
| Consumidores | `3` | la energía y los tokens de cada consumidor que identifica el proxy en el último día |
| Fuentes | `4` | cada canal de energía, si se usa y por qué no, y los problemas encontrados |
| Alertas | `5` | las [alertas](../alerts/) disparadas o pendientes, luego las resueltas en el último día |

| Tecla | Acción |
|---|---|
| `Tab`, `Shift+Tab`, `1` a `5` | cambia de vista |
| `Arriba`, `Abajo`, `j`, `k` | se mueve en una tabla |
| `s` | cambia la columna de orden |
| `/` | filtra las filas; `Enter` conserva el filtro, `Esc` lo quita |
| `p` | pausa la actualización |
| `e` | exporta la vista a un archivo CSV en el directorio actual |
| `?` | ayuda de la vista |
| `q` | salir |

Funciona por SSH desde 80 por 24 caracteres. Cuando el locale no es UTF-8, o
con `--ascii`, dibuja solo con ASCII; con `--no-color` o `NO_COLOR` dibuja sin
color y distingue las líneas de la gráfica por sus marcas. La esquina
inferior derecha muestra la CPU y la memoria que usa el propio `top`.
