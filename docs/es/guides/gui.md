---
title: "Usar la interfaz gráfica"
description: "Abre la interfaz en el navegador, agrégala al escritorio y lee qué muestra cada pantalla."
weight: 30
---

El agente atiende una interfaz gráfica en la misma dirección que su API
HTTP, `127.0.0.1:10060` de forma predeterminada. No necesita nada de
internet: las fuentes, las gráficas y esta documentación son parte del
binario.

## Abrirla

Con el agente en ejecución, en la misma máquina:

```bash
greenlens gui
```

El comando pide al agente un enlace de inicio de sesión de un solo uso y lo
abre en el navegador predeterminado. El enlace funciona una vez, dentro de
dos minutos. `--role viewer` o `--role operator` inicia la sesión del
navegador con menos permisos que el `admin` predeterminado, y `--print`
imprime el enlace en lugar de abrirlo.

Por SSH, `greenlens gui` imprime el enlace junto con el reenvío de puerto
que debes ejecutar en tu computadora:

```bash
ssh -N -L 10060:127.0.0.1:10060 server.example.com
```

Desde otra máquina sin túnel, el agente debe escuchar en la red y el
navegador inicia sesión con un token de acceso; consulta
[Usar la API HTTP y los tokens de acceso](../http-api/).

Cuando la API HTTP está apagada (`server.enabled: false`), el comando se
detiene con `GL4015`.

## Agregarla al escritorio

El lanzador ejecuta `greenlens gui`. Instálalo para tu usuario:

```bash
install -D -m 0644 deploy/desktop/greenlens.desktop ~/.local/share/applications/greenlens.desktop
install -D -m 0644 deploy/desktop/greenlens.svg ~/.local/share/icons/hicolor/scalable/apps/greenlens.svg
```

Iniciado desde el lanzador, un error aparece como notificación del
escritorio cuando `notify-send` está instalado.

Los navegadores también pueden instalar la interfaz como aplicación desde su
menú, en `127.0.0.1` o con TLS. La ventana instalada guarda sus propios
archivos, así que abre y avisa que el agente no responde en lugar de mostrar
un error del navegador. Las mediciones siempre vienen del agente y el
navegador nunca las guarda.

## Pantallas

| Pantalla | Muestra |
|---|---|
| Resumen | la potencia del nodo ahora y en los últimos minutos, por dominio, y la energía del último día, semana o mes |
| Backends | cada servidor de inferencia encontrado o declarado, con su potencia, tokens por segundo y energía por token |
| Modelos | la energía por token generado y el SCI de cada modelo, su historial, la división entre prefill y decode, y los días que son regresiones junto a los cambios de contexto |
| Consumidores | la energía y los tokens de cada consumidor identificado por el proxy |
| Benchmarks | ejecuta un benchmark contra un backend, compara dos resultados y expórtalos |
| Reportes | la energía, las emisiones, el costo y el agua de un periodo, por día u hora y por backend, modelo o consumidor, en CSV o JSON, con los reportes ambiental y SCI imprimibles |
| Fuentes | cada canal de energía, si se usa y por qué no |
| Configuración | los ajustes del agente, con una vista previa de cada cambio (solo administradores) |
| Ayuda | esta documentación, con búsqueda |
| Acerca de | la versión, la licencia, los componentes y lo que consume la propia interfaz |

La primera visita muestra unos primeros pasos, entre ellos una calibración
de la potencia en reposo cuando el agente aún no tiene una.

## Preferencias

El idioma, el tema (como el sistema, oscuro o claro) y el modo eficiente se
guardan en el navegador. El modo eficiente apaga el desenfoque y las
animaciones y redibuja las gráficas cada cinco segundos. En modo automático
se enciende cuando el dispositivo usa batería, o cuando el procesamiento de
la propia interfaz pasa de 1 W, según lo estima la pantalla Acerca de.

Todas las pantallas funcionan con el teclado, y las gráficas vienen con una
tabla de sus valores.
