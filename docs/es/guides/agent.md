---
title: "Ejecutar el agente"
description: "Ejecuta el agente como servicio del sistema o como usuario, y administra sus datos."
weight: 30
---

El agente mide de forma continua. Todos los demás comandos funcionan sin él,
excepto `status` y `report`, que leen lo que guardó.

## Como servicio del sistema

Instala el binario como `/usr/bin/greenlens` y luego los archivos de
`deploy/`:

```bash
sudo install -m 0644 deploy/systemd/greenlens.sysusers /usr/lib/sysusers.d/greenlens.conf
sudo systemd-sysusers
sudo install -D -m 0644 deploy/config/greenlens.example.yaml /etc/greenlens/greenlens.yaml
sudo install -m 0644 deploy/systemd/greenlens.service /etc/systemd/system/greenlens.service
sudo greenlens setup-permissions --apply
sudo systemctl daemon-reload
sudo systemctl enable --now greenlens
```

El servicio se ejecuta como el usuario sin privilegios `greenlens`, con una
configuración de systemd endurecida, guarda su base de datos en
`/var/lib/greenlens` y escucha en `/run/greenlens/greenlens.sock`. Los
miembros del grupo `greenlens` pueden usar `greenlens status` y
`greenlens report`.

El agente además se limita con Landlock en los kernels que lo soportan: solo
puede leer `/sys`, `/proc` y su configuración, y solo escribir en sus
directorios de datos y del socket.

| Tarea | Comando |
|---|---|
| Recargar la configuración | `sudo systemctl reload greenlens` |
| Seguir los registros | `journalctl -u greenlens -f` |
| Detener | `sudo systemctl stop greenlens` |

Por defecto el servicio solo puede conectarse a localhost. El drop-in
`deploy/systemd/greenlens-network.conf` permite las redes internas de tus
backends o medidores.

## Como usuario, sin instalación

```bash
greenlens agent
```

La base de datos va a `$XDG_DATA_HOME/greenlens` y el socket a
`$XDG_RUNTIME_DIR/greenlens/greenlens.sock`. `greenlens status` busca primero
el agente del usuario y luego el servicio del sistema.

## Datos

| Resolución | Se conserva por defecto | Ajuste |
|---|---|---|
| Cada muestra | 7 días | `retention.raw` |
| Un minuto | 90 días | `retention.rollup_1m` |
| Una hora | 2 años | `retention.rollup_1h` |

La base de datos es SQLite en modo WAL, legible solo por su dueño. El agente
revisa su integridad cada día y devuelve el espacio libre cada hora.
