---
title: "Leer los sensores sin root"
description: "Da al agente acceso a RAPL, perf_event e IPMI sin ejecutarlo como root."
weight: 20
---

GreenLens nunca se ejecuta como root. Desde el canal lateral Platypus
(CVE-2020-8694), el kernel deja los contadores de energía RAPL legibles solo
para root, así que hay que compartirlos con el agente de forma explícita. Hay
tres formas, en orden de preferencia.

## 1. Un grupo para los contadores de powercap

```bash
sudo greenlens setup-permissions --apply --user $USER
```

Esto crea el grupo `greenlens-power`, instala
`/etc/udev/rules.d/70-greenlens-power.rules`, lo aplica a los contadores
existentes y agrega el usuario al grupo. El servicio del sistema se ejecuta
con ese grupo como grupo suplementario. Los usuarios agregados al grupo
obtienen acceso desde su próximo inicio de sesión.

## 2. CAP_PERFMON para perf_event

Cuando no se pueden compartir los archivos de powercap, los mismos contadores
se leen a través de la PMU `power` de perf_event con la capacidad CAP_PERFMON:

```bash
sudo greenlens setup-permissions --apply --perfmon
```

Esto da la capacidad al binario, y hay que repetirlo después de cada
actualización del binario. Para el servicio del sistema, instala en su lugar
`deploy/systemd/greenlens-perfmon.conf` como drop-in.

## 3. El dispositivo IPMI

Los servidores con BMC reportan la potencia de todo el sistema por IPMI DCMI:

```bash
sudo greenlens setup-permissions --apply --ipmi
```

El servicio también necesita el drop-in `deploy/systemd/greenlens-ipmi.conf`
para abrir el dispositivo.

## El riesgo y cómo limitarlo

Leer contadores de energía con alta resolución puede revelar información de
lo que calculan otros procesos. Da el grupo solo al agente y a los
administradores que lo necesiten. Si tu política de seguridad lo exige, sube
`sources.rapl.min_interval` para reducir la resolución temporal que acepta
GreenLens.

Cada paso muestra su plan antes de cambiar algo, y `greenlens doctor` muestra
el resultado.
