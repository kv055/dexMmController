# Single stage: node:lts (Debian) required — openclaw installer rejects Alpine (musl)
FROM node:lts-slim

RUN apt-get update && apt-get install -y --no-install-recommends curl git ca-certificates \
    && rm -rf /var/lib/apt/lists/*

RUN useradd -m -s /bin/bash openclaw
USER openclaw
WORKDIR /home/openclaw

RUN curl -fsSL https://openclaw.ai/install.sh | bash -s -- --no-onboard

COPY --chown=openclaw:openclaw entrypoint.sh /home/openclaw/entrypoint.sh
RUN chmod +x /home/openclaw/entrypoint.sh

# Config is mounted at runtime: -v ./openclaw.json:/config/openclaw.json
VOLUME ["/config"]

EXPOSE 8001

ENTRYPOINT ["/home/openclaw/entrypoint.sh"]
