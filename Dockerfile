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

# Copy MCP python dependencies and files
COPY odooclaw/workspace/skills/odoo-mcp/requirements.txt /tmp/odoo-mcp-reqs.txt
RUN pip install --no-cache-dir --break-system-packages -r /tmp/odoo-mcp-reqs.txt || true
COPY odooclaw/workspace/skills/odoo-mcp/src /opt/odoo-mcp
ENV PYTHONPATH="/opt/odoo-mcp:${PYTHONPATH}"

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
