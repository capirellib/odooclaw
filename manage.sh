#!/bin/bash

# management script for OdooClaw multi-instance Docker containers
# Usage: ./manage.sh {build|create|start|stop|restart|logs|list} [instance_name]

# Exit immediately if a command exits with a non-zero status
set -e

BASE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
INSTANCES_DIR="${BASE_DIR}/instances"

show_help() {
    echo "OdooClaw Multi-Instance Docker Manager"
    echo ""
    echo "Usage:"
    echo "  $0 build                          - Build the shared OdooClaw Docker image"
    echo "  $0 create [instance_name]         - Create a new OdooClaw instance directory and config"
    echo "  $0 start [instance_name]          - Start the OdooClaw container for the instance"
    echo "  $0 stop [instance_name]           - Stop the OdooClaw container for the instance"
    echo "  $0 restart [instance_name]        - Restart the OdooClaw container for the instance"
    echo "  $0 logs [instance_name]           - View logs of the OdooClaw container"
    echo "  $0 list                           - List all instances and their status"
    echo ""
    echo "Examples:"
    echo "  $0 build"
    echo "  $0 create client-a"
    echo "  $0 start client-a"
    echo "  $0 logs client-a"
    exit 1
}

# Ensure command argument is provided
if [ -z "$1" ]; then
    show_help
fi

COMMAND="$1"
INSTANCE="$2"

# Helper: validate instance name is provided when needed
require_instance() {
    if [ -z "$INSTANCE" ]; then
        echo "Error: Instance name is required for command '$COMMAND'"
        echo "Usage: $0 $COMMAND [instance_name]"
        exit 1
    fi
}

case "$COMMAND" in
    build)
        echo "🐳 Building OdooClaw base Docker image..."
        BUILD_PLATFORM="${2}"
        PLATFORM_ARG=""
        if [ -n "${BUILD_PLATFORM}" ]; then
            echo "   Target Platform: ${BUILD_PLATFORM}"
            PLATFORM_ARG="--platform ${BUILD_PLATFORM}"
        else
            UNAME_S=$(uname -s)
            if [ "$UNAME_S" = "Darwin" ]; then
                echo "   Detected macOS host. Defaulting target platform to linux/amd64 for Linux compatibility."
                echo "   If you want to build native arm64, run: ./manage.sh build linux/arm64"
                PLATFORM_ARG="--platform linux/amd64"
            fi
        fi
        docker build $PLATFORM_ARG -t odooclaw:latest -f "${BASE_DIR}/Dockerfile" "${BASE_DIR}"
        echo "✅ Base image built successfully (odooclaw:latest)."
        ;;

    create)
        require_instance
        INSTANCE_DIR="${INSTANCES_DIR}/${INSTANCE}"
        if [ -d "$INSTANCE_DIR" ]; then
            echo "Error: Instance '${INSTANCE}' already exists at: ${INSTANCE_DIR}"
            exit 1
        fi

        echo "📁 Creating directory for instance '${INSTANCE}'..."
        mkdir -p "${INSTANCE_DIR}/workspace"
        mkdir -p "${INSTANCE_DIR}/.npm"

        # Create .env template
        cat <<EOF > "${INSTANCE_DIR}/.env"
# ─────────────────────────────────────────────
# OdooClaw Instance Configuration: ${INSTANCE}
# ─────────────────────────────────────────────

# Port for OdooClaw Gateway on host
GATEWAY_PORT=18790

# Google Gemini API key
GEMINI_API_KEY=

# Telegram Channel Configuration
TELEGRAM_ENABLED=false
TELEGRAM_TOKEN=
TELEGRAM_USER_ID=

# WhatsApp Channel Configuration (Evolution API)
WHATSAPP_ENABLED=false
WHATSAPP_BRIDGE_URL=ws://localhost:3001

# Remote Odoo Server Configuration
ODOO_URL=http://localhost:8069
ODOO_DB=
ODOO_USERNAME=
ODOO_PASSWORD=
EOF

        # Copy the template config.json
        cp "${BASE_DIR}/config.template.json" "${INSTANCE_DIR}/config.json"
        
        echo "✅ Instance '${INSTANCE}' created successfully."
        echo "👉 Next steps:"
        echo "  1. Edit configuration parameters in: ${INSTANCE_DIR}/.env"
        echo "  2. Start the instance using: $0 start ${INSTANCE}"
        ;;

    start)
        require_instance
        INSTANCE_DIR="${INSTANCES_DIR}/${INSTANCE}"
        if [ ! -d "$INSTANCE_DIR" ]; then
            echo "Error: Instance '${INSTANCE}' does not exist. Create it first using: $0 create ${INSTANCE}"
            exit 1
        fi

        echo "🔄 Processing configuration for '${INSTANCE}'..."
        
        # Parse the .env file and update config.json using Python
        python3 -c "
import os, json
from pathlib import Path

instance_dir = Path('$INSTANCE_DIR')
env_file = instance_dir / '.env'
config_file = instance_dir / 'config.json'

# Load env variables from .env file manually
env_vars = {}
if env_file.exists():
    with open(env_file, 'r') as f:
        for line in f:
            line = line.strip()
            if not line or line.startswith('#'):
                continue
            parts = line.split('=', 1)
            if len(parts) == 2:
                key, val = parts[0].strip(), parts[1].strip()
                env_vars[key] = val

# Read template/config file
with open(config_file, 'r') as f:
    config = json.load(f)

# Update config values based on env file
config['model_list'][0]['api_key'] = env_vars.get('GEMINI_API_KEY', '')
config['channels']['telegram']['enabled'] = env_vars.get('TELEGRAM_ENABLED', 'false').lower() == 'true'
config['channels']['telegram']['token'] = env_vars.get('TELEGRAM_TOKEN', '')
if env_vars.get('TELEGRAM_USER_ID'):
    config['channels']['telegram']['allow_from'] = [env_vars.get('TELEGRAM_USER_ID')]
else:
    config['channels']['telegram']['allow_from'] = []

config['channels']['whatsapp']['enabled'] = env_vars.get('WHATSAPP_ENABLED', 'false').lower() == 'true'
config['channels']['whatsapp']['bridge_url'] = env_vars.get('WHATSAPP_BRIDGE_URL', '')

config['channels']['odoo']['target_db'] = env_vars.get('ODOO_DB', '')

# Update Odoo MCP Server configs
mcp_servers = config.get('tools', {}).get('mcp', {}).get('servers', {})
if 'odoo-manager' in mcp_servers:
    odoo_env = mcp_servers['odoo-manager'].get('env', {})
    odoo_env['ODOO_URL'] = env_vars.get('ODOO_URL', '')
    odoo_env['ODOO_DB'] = env_vars.get('ODOO_DB', '')
    odoo_env['ODOO_USERNAME'] = env_vars.get('ODOO_USERNAME', '')
    odoo_env['ODOO_PASSWORD'] = env_vars.get('ODOO_PASSWORD', '')

# Save config
with open(config_file, 'w') as f:
    json.dump(config, f, indent=2)
"

        # Load GATEWAY_PORT from .env, default to 18790
        GATEWAY_PORT=$(grep -E "^GATEWAY_PORT=" "${INSTANCE_DIR}/.env" | cut -d= -f2 || echo "18790")

        echo "🚀 Starting OdooClaw container for '${INSTANCE}' on port ${GATEWAY_PORT}..."
        export INSTANCE_NAME="${INSTANCE}"
        export GATEWAY_PORT="${GATEWAY_PORT}"
        docker compose -f "${BASE_DIR}/docker-compose.yml" -p "odooclaw-${INSTANCE}" up -d
        echo "✅ OdooClaw instance '${INSTANCE}' is running."
        ;;

    stop)
        require_instance
        INSTANCE_DIR="${INSTANCES_DIR}/${INSTANCE}"
        if [ ! -d "$INSTANCE_DIR" ]; then
            echo "Error: Instance '${INSTANCE}' does not exist."
            exit 1
        fi

        GATEWAY_PORT=$(grep -E "^GATEWAY_PORT=" "${INSTANCE_DIR}/.env" | cut -d= -f2 || echo "18790")

        echo "🛑 Stopping OdooClaw container for '${INSTANCE}'..."
        export INSTANCE_NAME="${INSTANCE}"
        export GATEWAY_PORT="${GATEWAY_PORT}"
        docker compose -f "${BASE_DIR}/docker-compose.yml" -p "odooclaw-${INSTANCE}" down
        echo "✅ OdooClaw instance '${INSTANCE}' stopped."
        ;;

    restart)
        require_instance
        $0 stop "$INSTANCE"
        $0 start "$INSTANCE"
        ;;

    logs)
        require_instance
        docker logs -f "odooclaw-${INSTANCE}"
        ;;

    list)
        echo "📋 Available OdooClaw Instances:"
        echo "═══════════════════════════════════════════════════════════"
        if [ ! -d "$INSTANCES_DIR" ] || [ -z "$(ls -A "$INSTANCES_DIR" 2>/dev/null)" ]; then
            echo "No instances found. Create one using: $0 create [name]"
        else
            for dir in "$INSTANCES_DIR"/*; do
                if [ -d "$dir" ]; then
                    name=$(basename "$dir")
                    status="Stopped"
                    if docker ps --format '{{.Names}}' | grep -q "^odooclaw-${name}$"; then
                        port=$(grep -E "^GATEWAY_PORT=" "${dir}/.env" | cut -d= -f2 || echo "18790")
                        status="Running (Port ${port})"
                    fi
                    echo "  - ${name}: ${status}"
                fi
            done
        fi
        echo "═══════════════════════════════════════════════════════════"
        ;;

    *)
        show_help
        ;;
esac
