# Guía de Despliegue y Verificación en VPS

Los dos servicios corren en stacks de Dockge separados:

| Servicio | Stack (carpeta) | Servicio en compose | Contenedor | Imagen |
|---|---|---|---|---|
| OdooClaw | `/opt/stacks/proebainicialodooclaw` | `odooclaw` | `odooclaw-prueba` | `bettaerp/odooclaw:latest` |
| Betta AI Reseller | `/opt/stacks/betta_ai_openrouter_service` | `openrouter-reseller` | `openrouter-reseller-service` | `bettaerp/betta_ai_openrouter_service:latest` |

> **Importante:** los `compose.yaml` usan `pull_policy: never`. Con esa política,
> el botón **Actualizar** de Dockge y `docker compose pull` **no descargan** la
> imagen (el log muestra `Skipped`) y el contenedor se reinicia con la imagen
> vieja. Siempre hay que hacer `docker pull` a mano antes de recrear el contenedor.

## 1. Actualizar OdooClaw

```bash
docker pull bettaerp/odooclaw:latest
cd /opt/stacks/proebainicialodooclaw
docker compose up -d odooclaw
```

## 2. Actualizar Betta AI Reseller

```bash
docker pull bettaerp/betta_ai_openrouter_service:latest
cd /opt/stacks/betta_ai_openrouter_service
docker compose up -d openrouter-reseller
```

Desde Dockge: primero el `docker pull` en la consola de la VPS y después
**Actualizar** en el stack `betta_ai_openrouter_service`.

## 3. Verificar que el contenedor usa la última imagen (los 2 IDs deben coincidir)

```bash
# OdooClaw:
docker inspect odooclaw-prueba --format 'ID en el contenedor: {{.Image}}' && docker image inspect bettaerp/odooclaw:latest --format 'ID imagen en VPS:     {{.Id}}'

# Betta AI Reseller:
docker inspect openrouter-reseller-service --format 'ID en el contenedor: {{.Image}}' && docker image inspect bettaerp/betta_ai_openrouter_service:latest --format 'ID imagen en VPS:     {{.Id}}'
```

El reseller además informa su versión. Debe coincidir con `Version` en
`betta_ai_openrouter_service/internal/webadmin/version.go` del commit desplegado:

```bash
curl -s http://127.0.0.1:8088/healthz
```

Si sigue mostrando la versión anterior, la imagen nueva no llegó a Docker Hub
(ver `COMPILACION_Y_SUBIDA_DOCKER.md`) o se omitió el `docker pull`.

## 4. Ver estado y logs

```bash
docker ps --filter name=odooclaw
docker logs --tail=20 odooclaw-prueba

docker ps --filter name=openrouter-reseller
docker logs --tail=50 openrouter-reseller-service
```

En el log del reseller, las líneas `periodic Odoo export ... failed` indican que
la exportación automática de saldos a Odoo no pudo completarse (por ejemplo, API
key de Odoo vencida).

## 5. Después de actualizar el módulo `betta_ai_reseller` en Odoo

Si el despliegue del código de Odoo no ejecuta `-u betta_ai_reseller`, los
cambios de vistas XML no se aplican hasta actualizar el módulo desde Apps. Las
columnas nuevas de `res.partner` las crea el propio módulo al reiniciar Odoo.
