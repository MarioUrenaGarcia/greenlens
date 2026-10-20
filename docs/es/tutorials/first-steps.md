---
title: "Primeros diez minutos"
description: "Compila GreenLens, deja que lea los contadores de energía, calibra y mide un comando."
weight: 10
---

Este tutorial te lleva del código fuente a tu primera medición. Toma unos
diez minutos en un equipo Linux con procesador Intel o AMD.

## Qué necesitas

- Linux en amd64 o arm64, en hardware físico para medir energía real. En una
  máquina virtual GreenLens también funciona, con valores estimados.
- Go en la versión que declara `go.mod`, git y make.
- Una cuenta de administrador para un paso: dar al agente acceso de lectura a
  los contadores de energía.

## 1. Compila el binario

```bash
git clone https://github.com/MarioUrenaGarcia/greenlens.git
cd greenlens
make build
```

El binario queda en `bin/greenlens`. Está enlazado de forma estática, así que
puedes copiarlo a cualquier equipo Linux de la misma arquitectura.

## 2. Mira lo que ofrece el equipo

```bash
bin/greenlens doctor
```

`doctor` lista las fuentes de energía que encontró, cuáles usaría y por qué no
usa las demás. En un equipo recién instalado suele reportar `GL1002`: los
contadores de energía RAPL solo los puede leer root.

## 3. Permite que GreenLens lea los contadores sin root

Primero revisa el plan; todavía no cambia nada:

```bash
bin/greenlens setup-permissions --user $USER
```

Después aplícalo:

```bash
sudo bin/greenlens setup-permissions --apply --user $USER
```

El plan crea el grupo `greenlens-power`, instala una regla de udev que permite
a ese grupo leer los contadores y agrega tu usuario al grupo. Cierra sesión y
vuelve a entrar para que tu sesión tenga el grupo nuevo, y revisa:

```bash
bin/greenlens doctor
```

La tabla de fuentes ahora muestra `rapl` en uso, con calidad `medido`.

## 4. Inicia el agente

En una segunda terminal:

```bash
bin/greenlens agent
```

El agente muestrea la energía cada segundo, la guarda y atiende a los demás
comandos. Revísalo desde la primera terminal:

```bash
bin/greenlens status
```

## 5. Calibra la potencia en reposo

Cierra otros programas y deja el equipo quieto durante un minuto:

```bash
bin/greenlens calibrate
```

La mediana de ese minuto se vuelve la línea base de reposo. Con la política
`dynamic` por defecto, GreenLens atribuye a los procesos solo la energía por
encima de ella.

## 6. Mide un comando

```bash
bin/greenlens measure -- sh -c 'timeout 10 sha256sum /dev/zero; true'
```

Cuando el comando termina, `measure` reporta la energía que se le atribuye, su
potencia promedio, sus emisiones con el factor de emisión FESEN 2024 y la
calidad de los valores. La [guía de measure](../../guides/measure/) explica
cada línea.

## 7. Reporta un periodo

```bash
bin/greenlens report --from 1h
```

Ya tienes al agente midiendo el equipo. La [guía del agente](../../guides/agent/)
muestra cómo ejecutarlo como servicio del sistema.
