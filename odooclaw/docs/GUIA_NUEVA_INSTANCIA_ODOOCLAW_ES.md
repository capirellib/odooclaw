# Guía operativa: nueva instancia OdooClaw por cliente

Esta guía sirve para dar de alta una instancia independiente de OdooClaw para cada cliente desde Dockge, conectarla con su Odoo y publicarla de forma segura mediante Cloudflare Tunnel.

## Resultado esperado

Cada cliente tendrá su propio contenedor, sus propias credenciales y una URL pública diferente.

```text
Odoo del cliente
    |
    | HTTPS: https://claw-CLIENTE.bettaerp.com/webhook/odoo
    v
Cloudflare Tunnel (bettaclaw)
    |
    | HTTP interno: localhost:PUERTO_DEL_CLIENTE
    v
OdooClaw del cliente en Dockge
    |
    v
OpenRouter y API de Odoo del cliente
```

No se instala Cloudflare en la VPS de Odoo del cliente. `cloudflared` se ejecuta solamente en la VPS central donde viven Dockge y OdooClaw. Odoo se comunica con la URL HTTPS pública del subdominio.

## 1. Definir los datos de la instancia

Antes de crear nada, preparar una ficha por cliente. Nunca reutilizar claves, usuarios, base de datos ni subdominios de otro cliente.

| Dato | Ejemplo | Regla |
|---|---|---|
| Nombre en Dockge | `odooclaw-acme` | Minúsculas, sin espacios |
| Nombre del contenedor | `odooclaw-acme` | Único |
| Puerto interno del host | `18791` | Único por instancia |
| Subdominio | `claw-acme.bettaerp.com` | Único por instancia |
| URL de Odoo | `https://odoo.cliente.com` | URL pública y HTTPS |
| Base de datos | `acme_prod` | Base exacta de Odoo |
| Usuario técnico | `odooclaw@cliente.com` | Exclusivo para el bot |
| Clave de OpenRouter | clave del cliente o clave con límite | No compartir entre clientes |

## 2. Preparar Odoo del cliente

1. Instalar el módulo `mail_bot_odooclaw` compatible con Odoo 19.
2. Actualizar la lista de aplicaciones e instalar el módulo.
3. Crear un usuario técnico exclusivo para OdooClaw. Debe tener únicamente los permisos necesarios para las acciones que el bot realizará.
4. Anotar URL, base de datos, usuario y contraseña para el archivo `.env` de esta instancia.
5. No configurar aún el parámetro `odooclaw.webhook_url`; se completa al crear la ruta de Cloudflare.

## 3. Crear la instancia en Dockge

En Dockge, crear un nuevo Compose con el nombre elegido. Usar una imagen fijada por digest para evitar cambios inesperados y errores de descarga por límite de Docker Hub.

Ejemplo para `acme`, que usa el puerto local `18791`:

```yaml
services:
  odooclaw:
    image: bettaerp/odooclaw@sha256:a5d330162be6a80e980fc7480fd5859b5d0e733158e57365cdcdc94df5a91d2f
    pull_policy: never
    container_name: odooclaw-acme
    restart: unless-stopped

    # Temporal para la primera prueba si el volumen no tiene permisos.
    # Quitar esta línea antes de producción tras corregir propietario/permisos.
    user: "0:0"

    entrypoint:
      - /bin/sh
      - -ec
      - |
        mkdir -p /root/.odooclaw
        cat > /root/.odooclaw/config.json <<EOF
        {
          "agents": {
            "defaults": {
              "workspace": "/home/odooclaw/.odooclaw/workspace",
              "restrict_to_workspace": true,
              "model_name": "openrouter-free"
            }
          },
          "model_list": [
            {
              "model_name": "openrouter-free",
              "model": "openrouter/free",
              "api_key": "$${OPENROUTER_API_KEY}",
              "api_base": "https://openrouter.ai/api/v1"
            }
          ],
          "channels": {
            "odoo": {
              "enabled": true,
              "webhook_host": "0.0.0.0",
              "webhook_port": 18790,
              "webhook_path": "/webhook/odoo",
              "target_db": "$${ODOO_DB}",
              "allow_group_mentions": true
            }
          },
          "gateway": {"host": "0.0.0.0", "port": 18790}
        }
        EOF
        exec odooclaw gateway

    environment:
      ODOOCLAW_CONFIG: /root/.odooclaw/config.json
      OPENROUTER_API_KEY: ${OPENROUTER_API_KEY}
      ODOO_URL: ${ODOO_URL}
      ODOO_DB: ${ODOO_DB}
      ODOO_USERNAME: ${ODOO_USERNAME}
      ODOO_PASSWORD: ${ODOO_PASSWORD}

    # Solo accesible desde la VPS; no publicar 0.0.0.0:18791.
    ports:
      - "127.0.0.1:18791:18790"

    volumes:
      - odooclaw_acme_workspace:/home/odooclaw/.odooclaw/workspace
      - odooclaw_acme_npm:/home/odooclaw/.npm

volumes:
  odooclaw_acme_workspace:
  odooclaw_acme_npm:
```

### Archivo `.env` de Dockge

Crear o completar el archivo `.env` asociado a esa pila. Reemplazar todos los valores de ejemplo; no pegar secretos en el `compose.yaml` ni en capturas de pantalla.

```dotenv
OPENROUTER_API_KEY=sk-or-v1-REEMPLAZAR
ODOO_URL=https://odoo.cliente.com
ODOO_DB=acme_prod
ODOO_USERNAME=odooclaw@cliente.com
ODOO_PASSWORD=REEMPLAZAR
```

Guardar y desplegar. Dockge debe mostrar el contenedor como `running`.

### Validación local

En Dockge debe verse un enlace como `127.0.0.1:18791`. Los registros deben contener estas dos señales:

```text
Shared HTTP server listening {scheme=http, addr=0.0.0.0:18790}
Response: HEARTBEAT_OK
```

La segunda confirma que OdooClaw pudo obtener respuesta del proveedor de IA. Un error con `model "" not found` indica que el `entrypoint` o el archivo de configuración no fueron aplicados.

## 4. Crear la ruta en Cloudflare Tunnel

En Cloudflare One:

1. Ir a **Redes → Conectores → bettaclaw**.
2. Abrir **Rutas de aplicaciones publicadas**.
3. Elegir **Añadir ruta de aplicación publicada**.
4. Completar:

| Campo | Valor para el ejemplo |
|---|---|
| Subdominio | `claw-acme` |
| Dominio | `bettaerp.com` |
| Ruta | Vacía |
| Tipo | `HTTP` |
| URL | `localhost:18791` |

5. Guardar. Cloudflare crea el registro DNS automáticamente.

La relación puerto/subdominio debe mantenerse: `claw-acme` siempre apunta a `localhost:18791`; la siguiente instancia puede usar `claw-otro-cliente` y `localhost:18792`.

No aplicar Cloudflare Access al webhook durante la primera prueba: bloquearía la solicitud automática de Odoo. Si luego se protege con Access, se debe configurar una autenticación de servicio compatible con el módulo.

## 5. Vincular Odoo con el webhook público

En el Odoo del cliente, activar modo desarrollador y abrir:

**Ajustes → Técnico → Parámetros → Parámetros del sistema**.

Crear o modificar:

```text
Clave:  odooclaw.webhook_url
Valor:  https://claw-acme.bettaerp.com/webhook/odoo
```

Guardar. Esta es la única dirección que debe conocer Odoo para enviar mensajes al bot; no usar IPs privadas, `localhost` ni el puerto `18790` en ese parámetro.

## 6. Prueba de extremo a extremo

1. Confirmar que el túnel `bettaclaw` figure como **Óptimo** en Cloudflare.
2. Abrir `https://claw-acme.bettaerp.com/webhook/odoo` en un navegador. Un `404` o `405` puede ser normal porque el webhook espera una petición de Odoo; un `502`, `1033` o pantalla de error de túnel no lo es.
3. En Odoo, abrir un canal de Discusiones y mencionar a **BettaClaw**.
4. Mirar los registros de la pila en Dockge. Debe recibirse el webhook y generarse una respuesta.
5. Verificar que el bot solo accede a la base de datos y al usuario técnico del cliente correcto.

## Diagnóstico rápido

| Síntoma | Causa probable | Acción |
|---|---|---|
| `429 Too Many Requests` al desplegar | Límite de Docker Hub | Usar digest fijado y `pull_policy: never` si la imagen ya existe en la VPS. |
| `model "" not found in model_list` | Configuración no aplicada | Revisar que el contenedor use el `entrypoint` y `ODOOCLAW_CONFIG`. |
| `permission denied` en `workspace/state` | Propietario del volumen | Usar `user: "0:0"` solo para prueba y corregir permisos antes de producción. |
| `502 Bad Gateway` en el subdominio | Cloudflare no llega al puerto local | Revisar que el Compose publique `127.0.0.1:PUERTO:18790` y que la ruta use `localhost:PUERTO`. |
| El bot no responde en Odoo | URL de webhook o módulo | Revisar `odooclaw.webhook_url`, registros de Dockge y la instalación del módulo. |
| OpenRouter devuelve error de crédito o límite | Cuenta/modelo sin disponibilidad | Revisar créditos, límites y modelo seleccionado en OpenRouter. |

## Checklist antes de producción

- [ ] Usuario técnico distinto por cliente y con privilegios mínimos.
- [ ] Base de datos, credenciales, volúmenes, puerto y subdominio exclusivos.
- [ ] Clave de OpenRouter con límite de gasto o proyecto independiente.
- [ ] La ruta Cloudflare usa `localhost:PUERTO`, no una IP pública.
- [ ] El puerto publicado se restringe a `127.0.0.1`.
- [ ] Se reemplazó la ejecución temporal como root por permisos correctos del volumen.
- [ ] Se probaron mensajes, errores y permisos del bot en un entorno no productivo.
- [ ] Se guardaron las claves en un gestor de secretos y se revocaron las expuestas durante pruebas.

