---
title: "Elegir el idioma"
description: "Selecciona inglés o español para todos los mensajes."
weight: 10
---

GreenLens muestra cada mensaje, texto de ayuda y error en inglés o en español.
Usa el primero de estos ajustes que esté definido:

| Ajuste | Ejemplo |
|---|---|
| Opción `--lang` | `greenlens --lang es version` |
| Variable de entorno `GREENLENS_LANG` | `GREENLENS_LANG=es greenlens version` |
| Idioma del sistema: `LC_ALL`, `LC_MESSAGES` o `LANG` | `LANG=es_MX.UTF-8` |

Los valores aceptados son `en`, `es` y `auto`, que usa el idioma del sistema.
Las variables de idioma siguen el orden de POSIX: decide la primera que esté
definida.

Un valor no disponible en `--lang` o en `GREENLENS_LANG` es un error con código
[GL0008](../../reference/errors/#gl0008), que se muestra en el idioma del
sistema. Un idioma del sistema no disponible, o el idioma `C`, usa inglés sin
error.
