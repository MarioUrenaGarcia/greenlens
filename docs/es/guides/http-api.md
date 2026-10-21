---
title: "Usar la API HTTP y los tokens de acceso"
description: "Llega a la API y a la interfaz desde programas y otras máquinas, con roles y tokens de acceso."
weight: 57
---

El agente atiende su API HTTP, la interfaz gráfica y las métricas de
Prometheus en `127.0.0.1:10060`. Los comandos en la misma máquina usan en su
lugar el socket Unix y no necesitan token.

## Quién puede hacer qué

Toda petición necesita una sesión o un token de acceso, salvo `/healthz`,
`/readyz` y `/metrics`. Loopback no es la excepción, porque cualquier usuario
local puede llegar a `127.0.0.1`.

| Rol | Puede |
|---|---|
| `viewer` | leer mediciones, reportes, backends, fuentes y benchmarks |
| `operator` | además ejecutar benchmarks y calibraciones |
| `admin` | además gestionar tokens, leer el registro de auditoría y descargar respaldos |

Cada acción de un operador o de un administrador queda en el registro de
auditoría con el token o el usuario local que la hizo.

## Tokens de acceso

```bash
greenlens token create --name grafana --role viewer --expires 90d
greenlens token list
greenlens token revoke grafana
```

El secreto se imprime una sola vez; GreenLens guarda solo su hash argon2id,
en `tokens` dentro del directorio de datos (`server.tokens_file` indica otro
archivo). Envíalo como token bearer:

```bash
curl -H "Authorization: Bearer $GREENLENS_TOKEN" http://127.0.0.1:10060/api/v1/status
```

Con el agente en ejecución, `greenlens token` pasa por él, así que el cambio
aparece de inmediato en el registro de auditoría. Sin agente cambia el
archivo directamente; para el servicio del sistema ejecútalo como el usuario
del servicio:

```bash
sudo -u greenlens greenlens token create --name admin --role admin
```

## Escuchar en la red

Pon en `server.listen` una dirección de la máquina, como `192.0.2.10:10060`,
y permite la dirección en el drop-in de red de la unidad de systemd. El agente
se niega a arrancar en una dirección fuera de loopback mientras no haya ningún
token de acceso, así que crea uno primero. Dale a la API un certificado con
`server.tls.cert_file` y `server.tls.key_file`; sin TLS, los tokens y las
cookies de sesión cruzan la red sin cifrar.

Desde otra máquina, un túnel SSH mantiene la API en loopback:

```bash
ssh -L 10060:127.0.0.1:10060 server.example.com
```

## Sesiones del navegador

Un navegador inicia sesión con un token en la página de inicio de sesión, o
con un enlace de un solo uso que el agente da a los usuarios locales. La
cookie de sesión es `HttpOnly` y `SameSite=Strict`, y `Secure` con TLS. Una
sesión termina tras una semana sin uso y a los 30 días en cualquier caso. Las
peticiones de una sesión que cambian algo deben llevar su token CSRF y venir
del mismo origen.

## Límites

Cada dirección puede enviar 30 peticiones por segundo, con ráfagas de 60, y 10
intentos de inicio de sesión por minuto; los archivos de la interfaz no
cuentan. El cuerpo de las peticiones se limita a 1 MiB y las cabeceras a
32 KiB.
