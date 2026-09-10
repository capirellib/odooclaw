# ============================================================
# Stage 1: Build the odooclaw binary
# ============================================================
FROM golang:1.26.0-alpine AS builder

RUN apk add --no-cache git make

WORKDIR /src

# Cache dependencies
COPY odooclaw/go.mod odooclaw/go.sum ./
RUN go mod download

# Copy source and build
COPY odooclaw/ .
RUN make build

# ============================================================
# Stage 2: Node.js-based runtime with full MCP support
# ============================================================
FROM node:24-alpine3.23

# Install runtime dependencies
RUN apk add --no-cache \
  ca-certificates \
  curl \
  git \
  python3 \
  py3-pip \
  ffmpeg \
  tzdata

# Install uv and symlink to system path
RUN curl -LsSf https://astral.sh/uv/install.sh | sh && \
  ln -s /root/.local/bin/uv /usr/local/bin/uv && \
  ln -s /root/.local/bin/uvx /usr/local/bin/uvx && \
  uv --version

# Create non-root user and group
RUN addgroup -S odooclaw && adduser -S odooclaw -G odooclaw -h /home/odooclaw


# Copy binary from builder
COPY --from=builder /src/build/odooclaw /usr/local/bin/odooclaw

# --- MCP python dependencies -------------------------------------------------
# Split in two layers on purpose.
#
# 1) What the MCP server needs to *start*. A failure here must break the build:
#    if these are missing the process dies on import, the gateway only sees
#    `calling "initialize": EOF`, and the real cause is three layers away.
#    The mcp pin is deliberate: server.py uses the v1 API
#    (`from mcp.server.fastmcp import FastMCP`), renamed to MCPServer in 2.x.
#    Without the pin pip resolves 2.x and the server never boots.
RUN pip install --no-cache-dir --break-system-packages \
        "mcp<2" \
        "pydantic>=2.0.0" \
        "requests>=2.31.0"

# 2) Extras used by individual tools (OCR, spreadsheets, analysis). Several of
#    them build from source under musl, so a failure is tolerated: it degrades
#    those tools instead of shipping a broken image.
COPY odooclaw/workspace/skills/odoo-mcp/requirements.txt /tmp/odoo-mcp-reqs.txt
RUN pip install --no-cache-dir --break-system-packages -r /tmp/odoo-mcp-reqs.txt \
    || echo "WARNING: optional MCP extras were not installed; tools depending on them will fail"

COPY odooclaw/workspace/skills/odoo-mcp/src /opt/odoo-mcp
ENV PYTHONPATH="/opt/odoo-mcp:${PYTHONPATH}"

# Smoke test: the exact import chain that failed in production. Catching it here
# turns a silent runtime EOF into a build error naming the real problem.
RUN python3 -c "from mcp.server.fastmcp import FastMCP; import odoo_mcp.server; print('MCP server imports OK')"

# Copy other MCP scripts if they exist
COPY odooclaw/workspace/skills/edge-tts/server.py /usr/local/bin/edge-tts-mcp.py
RUN chmod +x /usr/local/bin/edge-tts-mcp.py

COPY odooclaw/workspace/skills/whisper-stt/server.py /usr/local/bin/whisper-stt-mcp.py
RUN chmod +x /usr/local/bin/whisper-stt-mcp.py

# Create cache directory for whisper models and workspace
RUN mkdir -p /home/odooclaw/.cache /home/odooclaw/.odooclaw && \
    chown -R odooclaw:odooclaw /home/odooclaw

# Switch to non-root user
USER odooclaw
WORKDIR /home/odooclaw

# Run onboard to create initial directories and config
RUN /usr/local/bin/odooclaw onboard

ENTRYPOINT ["odooclaw"]
CMD ["gateway"]
