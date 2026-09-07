# OdooClaw + OpenRouter en Dockge

Esta guía configura una instancia de OdooClaw que usa OpenRouter como proveedor
de IA. Está pensada para un VPS con Dockge y, opcionalmente, Cloudflare Tunnel.

## 1. Por qué falla la configuración anterior

OdooClaw **no reconoce** estas variables genéricas:

```text
OPENROUTER_API_KEY
OPENAI_BASE_URL
```

La aplicación lee, en cambio:

```text
ODOOCLAW_PROVIDERS_OPENROUTER_API_KEY
ODOOCLAW_PROVIDERS_OPENROUTER_API_BASE
```

También debe definirse un modelo válido de OpenRouter. Para la primera prueba
use el router compatible `openrouter/free`; no use un nombre de modelo de Google
como si fuera directamente una variable de entorno de OdooClaw.

No use un `entrypoint` que genere un `config.json` incompleto. Las variables de
entorno siguientes son suficientes y no dejan la clave dentro de `config.json`.

## 2. Crear la clave en OpenRouter

1. Ingrese a [OpenRouter Keys](https://openrouter.ai/keys).
2. Cree una API key para esta instancia o cliente; no reutilice una clave sin
   límite para todos los clientes.
3. Si es producción, asigne un límite de gasto y fecha de vencimiento.
4. Copie la clave una única vez y guárdela en Bitwarden o en el gestor de
   secretos elegido. Nunca la publique en Dockge, capturas, Git o conversaciones.

Una clave válida normalmente comienza con `sk-or-`, pero no comparta su valor.

## 3. Archivo `.env` de la pila Dockge

En el editor de variables de entorno de Dockge, o en el archivo `.env` que usa
esa pila, cargue valores equivalentes a estos:

```dotenv
GATEWAY_PORT=18790

# Secreto: pegar aquí la clave real, sin comillas ni espacios.
OPENROUTER_API_KEY=sk-or-v1-REEMPLAZAR

# Instancia Odoo del cliente.
ODOO_URL=https://odoo-del-cliente.example
ODOO_DB=base_cliente
ODOO_USERNAME=odooclaw_service
ODOO_PASSWORD=REEMPLAZAR_POR_API_KEY_O_PASSWORD

# Opcional: habilitar autenticación entre Odoo y OdooClaw.
ODOO_WEBHOOK_TOKEN=REEMPLAZAR_POR_TOKEN_LARGO_ALEATORIO
```

Para una operación con varios clientes, cada pila debe tener su propio archivo
de variables, usuario técnico de Odoo, clave OpenRouter y token webhook.

## 4. Compose recomendado

Pegue este contenido en Dockge. El ejemplo usa el router `openrouter/free`.
Sirve para una prueba inicial, pero no para producción por sus límites y
disponibilidad variable.

```yaml
services:
  odooclaw:
    image: bettaerp/odooclaw:latest
    container_name: odooclaw-prueba
    restart: unless-stopped
    command: ["gateway"]
    ports:
      # Si cloudflared se ejecuta en el host VPS, publíquelo sólo localmente.
      - "127.0.0.1:${GATEWAY_PORT:-18790}:18790"
    environment:
      # OpenRouter: éstos son los nombres que OdooClaw reconoce.
      ODOOCLAW_PROVIDERS_OPENROUTER_API_KEY: ${OPENROUTER_API_KEY}
      ODOOCLAW_PROVIDERS_OPENROUTER_API_BASE: https://openrouter.ai/api/v1
      ODOOCLAW_AGENTS_DEFAULTS_MODEL: openrouter/free

      # Gateway / canal Odoo.
      ODOOCLAW_GATEWAY_HOST: 0.0.0.0
      ODOOCLAW_GATEWAY_PORT: "18790"
      ODOOCLAW_CHANNELS_ODOO_ENABLED: "true"
      ODOOCLAW_CHANNELS_ODOO_WEBHOOK_PATH: /webhook/odoo
      ODOOCLAW_CHANNELS_ODOO_TARGET_DB: ${ODOO_DB}
      ODOOCLAW_CHANNELS_ODOO_ALLOW_GROUP_MENTIONS: "false"
      ODOOCLAW_CHANNELS_ODOO_WEBHOOK_TOKEN: ${ODOO_WEBHOOK_TOKEN}

      # Odoo al que OdooClaw responde.
      ODOO_URL: ${ODOO_URL}
      ODOO_DB: ${ODOO_DB}
      ODOO_USERNAME: ${ODOO_USERNAME}
      ODOO_PASSWORD: ${ODOO_PASSWORD}
    volumes:
      - ./workspace:/home/odooclaw/.odooclaw/workspace
      - ./.npm:/home/odooclaw/.npm
```

Si cloudflared está dentro de otro contenedor de la **misma red Docker**, no
publique el puerto y configure la ruta Cloudflare hacia
`http://odooclaw:18790`. Si cloudflared está instalado en el host, use
`http://localhost:18790` y conserve el mapeo `127.0.0.1:...` mostrado arriba.

## 5. Cloudflare y Odoo

Para el ejemplo anterior, cree una ruta pública del túnel:

```text
Hostname público: claw-prueba.bettaerp.com
Tipo: HTTP
Servicio de origen (cloudflared en host): http://localhost:18790
```

En Odoo, el parámetro del sistema es:

```text
Clave:   odooclaw.webhook_url
Valor:  https://claw-prueba.bettaerp.com/webhook/odoo
```

Si el módulo Odoo admite y utiliza token de webhook, configure además el mismo
valor de `ODOO_WEBHOOK_TOKEN` en el parámetro correspondiente del módulo. No
deje una ruta pública sin autenticación de entrada en un entorno productivo.

## 6. Verificación en orden

### A. Verificar que el contenedor recibió la configuración

En la consola del VPS ejecute:

```bash
docker logs --tail=200 odooclaw-prueba
```

Busque el inicio correcto del gateway y la ausencia de mensajes como `no API
key configured`. No copie registros que contengan secretos.

### B. Verificar la clave sin mostrarla

Desde el VPS, con la clave cargada sólo en su terminal, consulte información de
la clave:

```bash
curl -sS https://openrouter.ai/api/v1/key \
  -H "Authorization: Bearer $OPENROUTER_API_KEY"
```

Una respuesta JSON con `data` confirma autenticación. No agregue `-v` ni pegue
la salida completa si contiene información confidencial.

### C. Probar OpenRouter de forma aislada

```bash
curl -sS https://openrouter.ai/api/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -H "Authorization: Bearer $OPENROUTER_API_KEY" \
  -d '{"model":"openrouter/free","messages":[{"role":"user","content":"Responde solamente: OK"}]}'
```

Luego pruebe un mensaje directo al usuario **BettaClaw** en Discuss. Si responde,
la ruta completa Odoo → Cloudflare → OdooClaw → OpenRouter → Odoo funciona.

## 7. Diagnóstico de errores frecuentes

| Mensaje o síntoma | Causa probable | Corrección |
| --- | --- | --- |
| `no API key configured` | Se usó `OPENROUTER_API_KEY` directamente en `environment` o la variable quedó vacía. | Use `ODOOCLAW_PROVIDERS_OPENROUTER_API_KEY: ${OPENROUTER_API_KEY}` y vuelva a desplegar. |
| `401 Unauthorized` | La clave es inválida, revocada o contiene espacios/comillas. | Cree una clave nueva, cárguela en `.env`/Bitwarden y redepliegue. |
| `404` o `model ... not found` | El slug de modelo no existe o se configuró un alias incompleto. | Para confirmar conectividad use `openrouter/free`. Para un modelo concreto, valide previamente que la versión de OdooClaw lo envíe como el slug que OpenRouter publica. |
| `429 Too Many Requests` | Límite de velocidad, especialmente en modelos gratuitos. | Espere, reduzca concurrencia o use crédito/modelo pago. |
| Crédito insuficiente / `402` | El modelo elegido no es gratuito o la clave tiene límite de gasto agotado. | Cargue crédito, aumente el límite autorizado o seleccione temporalmente `openrouter/free`. |
| Error de DNS/TLS/conexión | El VPS no puede llegar a `openrouter.ai:443`, o hay proxy/firewall. | Pruebe el comando de la sección 6B desde el VPS y permita salida HTTPS. |
| El bot inicia pero usa otro modelo | Persiste un `config.json` con `model_name` distinto. | Elimine o corrija la configuración persistente; defina explícitamente `ODOOCLAW_AGENTS_DEFAULTS_MODEL`. |

## 8. Seguridad y operación

- Use una clave y un límite de gasto por cliente.
- No exponga `18790` a Internet: Cloudflare Tunnel debe ser el único ingreso.
- Use un usuario técnico de Odoo con permisos mínimos, nunca el administrador.
- Para producción, almacene las claves en Bitwarden Secrets Manager u otro
  gestor de secretos; `.env` es una solución transitoria y debe excluirse de Git.
- Al rotar una clave, actualice la variable de la pila y presione **Desplegar**
  en Dockge. Revóquela en OpenRouter sólo después de confirmar que la nueva
  funciona.

## 9. Nota sobre modelos concretos

La versión actual de OdooClaw admite sin ambigüedad los routers
`openrouter/free` y `openrouter/auto` mediante variables de entorno. Para usar
un slug concreto de OpenRouter (por ejemplo,
`dots-studio/dots-3-note-preview:free`) se debe validar el mapeo de protocolo y
nombre de modelo en la versión de la imagen antes de desplegarlo. No agregue un
prefijo inventado ni asuma que `google/...` configura la API de Google.

Como regla operativa, seleccione primero `openrouter/free` para comprobar clave,
red y gateway; luego fije un modelo de producción tras una prueba controlada y
con un plan de reemplazo. Los modelos de vista previa o gratuitos pueden
retirarse, cambiar de disponibilidad o aplicar límites de uso.

## Fuentes oficiales

- [Inicio rápido de OpenRouter](https://openrouter.ai/docs/quickstart)
- [Referencia de API y autenticación Bearer](https://openrouter.ai/docs/api_reference/overview)
- [Router de modelos gratuitos](https://openrouter.ai/docs/guides/routing/routers/free-router)
- [Información de la API key actual](https://openrouter.ai/docs/api/api-reference/api-keys/get-current-key)
