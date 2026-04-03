# node:lts-slim (Debian) required — openclaw installer rejects Alpine (musl)
FROM node:lts-slim

RUN apt-get update && apt-get install -y --no-install-recommends curl git ca-certificates \
    && rm -rf /var/lib/apt/lists/*

RUN useradd -m -s /bin/bash openclaw
USER openclaw
WORKDIR /home/openclaw

RUN curl -fsSL https://openclaw.ai/install.sh | bash -s -- --no-onboard

# Config is mounted at runtime: -v ./openclaw.json:/config/openclaw.json
VOLUME ["/config"]

EXPOSE 8001

ENTRYPOINT ["/bin/bash", "-c", "\
  if [ ! -f /config/openclaw.json ]; then \
    echo 'ERROR: No config file found at /config/openclaw.json'; \
    echo 'Mount your config: -v ./openclaw.json:/config/openclaw.json'; \
    exit 1; \
  fi; \
  mkdir -p ~/.openclaw && cp /config/openclaw.json ~/.openclaw/openclaw.json; \
  exec openclaw gateway"]
