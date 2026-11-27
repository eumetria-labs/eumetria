# The updater (PLAN phase 15b): updates Eumetria in place. The only
# container with the Docker socket; see internal/updater. Built from the
# repository root:
#   docker build -f deploy/updater/Dockerfile .

FROM golang:1.27 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY site ./site
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/eumetria-updater ./cmd/eumetria-updater
RUN mkdir -p /out/state

# docker compose itself, the official static binary. Pinned by digest;
# Dependabot proposes updates (.github/dependabot.yml).
FROM docker/compose-bin:v5.8.0@sha256:c9f99a4efa43ca83e5acc056da938c38504010c10a2dd804569032be5d3b0dd5 AS compose

# distroless/static as root: the Docker socket belongs to root (or the
# docker group, whose id differs between hosts). Root here is no more than
# what the socket gives anyway; the container has no ports, no capabilities
# and a read-only root file system (deploy/compose.yaml).
FROM gcr.io/distroless/static-debian12:latest@sha256:d75cdd72874d4790092fcb1b058493ecf6bb5bf2b2b897045b00ff01d91843f2
COPY --from=build /out/eumetria-updater /eumetria-updater
COPY --from=compose /docker-compose /usr/local/bin/docker-compose
COPY --from=build /out/state /state
VOLUME /state
ARG VERSION=dev
ARG REVISION=
LABEL org.opencontainers.image.source="https://github.com/eumetria-labs/eumetria" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${REVISION}"
EXPOSE 8080
HEALTHCHECK --interval=1m --timeout=10s --start-period=10s --start-interval=2s \
  CMD ["/eumetria-updater", "-healthcheck", "http://127.0.0.1:8080/healthz"]
ENTRYPOINT ["/eumetria-updater"]
