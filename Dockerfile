# syntax=docker/dockerfile:1
#
# Optional container image. The default fleet-box deployment is the bare
# binary under systemd (see docs/install.md); this image is the exception for
# boxes that prefer containers.
#
# To drive a rig that runs on the host, the container must see:
#   -v "$HOME/.openrig:/home/fleet/.openrig"
#   -v "$HOME/.ssh:/home/fleet/.ssh:ro"
#   -v "$HOME/.config/gh:/home/fleet/.config/gh:ro"
#   -v "$HOME/.config/glab-cli:/home/fleet/.config/glab-cli:ro"
#   -v "/run/user/$(id -u)/tmux-$(id -u):/tmp/tmux-1000"
#   -v "fleet-console-state:/home/fleet/.local/state/fleet-console"
#   -e FLEET_CONSOLE_TOKEN=...
# Without the rig state and tmux socket, the binary's startup check reports
# the rig as unreachable rather than driving a private daemon.

FROM node:24-bookworm AS web
WORKDIR /src
COPY package.json package-lock.json ./
RUN npm ci
COPY . .
RUN npm run build

FROM golang:1.25-bookworm AS build
WORKDIR /src
COPY --from=web /src/dist ./internal/webui/dist
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
ARG VERSION=0.1.0
ARG COMMIT=container
ARG BUILD_TIME=unknown
RUN CGO_ENABLED=0 go build -trimpath -tags embedboard \
    -ldflags "-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.buildTime=${BUILD_TIME}" \
    -o /out/fleet-console ./cmd/fleet-console

FROM node:24-bookworm
ARG GLAB_VERSION=1.124.0
ENV HOME=/home/fleet
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates curl git gh jq tmux \
    && rm -rf /var/lib/apt/lists/* \
    && curl -fsSL -o /tmp/glab.deb \
       "https://gitlab.com/gitlab-org/cli/-/releases/v${GLAB_VERSION}/downloads/glab_${GLAB_VERSION}_linux_amd64.deb" \
    && (dpkg -i /tmp/glab.deb || apt-get install -y -f) \
    && rm -f /tmp/glab.deb \
    && npm install -g @openrig/cli@0.7.0 \
    && mkdir -p /home/fleet/.local/state/fleet-console \
    && chown -R 1000:1000 /home/fleet
COPY --from=build /out/fleet-console /usr/local/bin/fleet-console
USER 1000:1000
EXPOSE 8787
ENTRYPOINT ["/usr/local/bin/fleet-console"]
CMD ["--addr", "0.0.0.0:8787", "--db", "/home/fleet/.local/state/fleet-console/fleet-console.db"]
