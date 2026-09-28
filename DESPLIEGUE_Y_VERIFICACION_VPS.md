# Guía de Despliegue y Verificación en VPS

## 1. Actualizar la imagen OdooClaw en la VPS

```bash
docker pull bettaerp/odooclaw:latest
cd /opt/stacks/proebainicialodooclaw
docker compose up -d odooclaw
```

## 2. Actualizar la imagen Betta AI Reseller en la VPS

```bash
docker pull bettaerp/betta-ai-reseller:latest
cd /opt/stacks/proebainicialodooclaw
docker compose up -d betta-ai-reseller
```

## 3. Comparar si el contenedor tiene la última imagen (deben coincidir los 2 IDs)

```bash
# Para OdooClaw:
docker inspect odooclaw-prueba --format 'ID en el contenedor: {{.Image}}' && docker image inspect bettaerp/odooclaw:latest --format 'ID imagen en VPS:     {{.Id}}'

# Para Betta AI Reseller:
docker inspect odooclaw-prueba --format 'ID en el contenedor: {{.Image}}' && docker image inspect bettaerp/betta-ai-reseller:latest --format 'ID imagen en VPS:     {{.Id}}'
```

## 4. Ver estado y logs

```bash
docker ps --filter name=odooclaw
docker logs --tail=20 odooclaw-prueba
```
