---
title: "Primeros pasos"
description: "Compila GreenLens desde el código fuente, ejecútalo y lee su ayuda y sus errores."
weight: 10
---

Este tutorial compila GreenLens desde el código fuente y muestra las partes de
la línea de comandos en las que se apoya cada función posterior: la ayuda, los
idiomas y los mensajes de error. Toma unos cinco minutos.

## Qué necesitas

- Un equipo Linux en amd64 o arm64.
- Go en la versión que declara `go.mod`, git y make.

## 1. Compila el binario

```bash
git clone https://github.com/MarioUrenaGarcia/greenlens.git
cd greenlens
make build
```

El binario queda en `bin/greenlens`. Está enlazado de forma estática, así que
puedes copiarlo a cualquier equipo Linux de la misma arquitectura.

## 2. Comprueba la compilación

```bash
bin/greenlens version
```

La salida muestra la versión, la revisión del código fuente, la versión de Go
y la plataforma del binario.

## 3. Lee la ayuda

```bash
bin/greenlens --help
```

Cada comando tiene su propia ayuda:

```bash
bin/greenlens help version
```

## 4. Cambia el idioma

GreenLens sigue el idioma de tu sistema. Para elegirlo de forma explícita:

```bash
bin/greenlens --lang es --help
```

## 5. Lee un error

Escribe mal un comando a propósito:

```bash
bin/greenlens --lang es verson
```

```text
error GL0002: comando desconocido 'verson'
  causa:    'verson' no es un comando de 'greenlens'
  solución: ¿quisiste decir 'version'? Ejecuta 'greenlens --help' para ver los comandos disponibles
```

Cada error tiene un código, una causa y una solución. La
[referencia de códigos de error](../../reference/errors/) los lista todos.
