# Self-hosted dashboard: one container serves the site and syncs on a schedule.
# See the docs: docs/src/content/docs/install/ (Docker Compose setup).

# golang:1.27 is always the newest 1.27 patch release (the build pulls it
# afresh), so security fixes of the standard library come with every build;
# CI rebuilds weekly for weeks without a push (pentest F13).
FROM golang:1.27 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
# The page files are compiled into the binary (site/embed.go names them;
# stats.json, photos, routes and days are never included).
COPY site ./site
# The version Settings → General and -version show: CI passes the release
# ("1.2.0") or "main-<commit>"; a build by hand is "dev".
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.appVersion=${VERSION}" -o /out/eumetria ./cmd/eumetria
# Empty data and backup folders for the image (distroless has no shell to
# make them there): owned by nonroot, so a named volume mounted on them
# (docker run -v eumetria-data:/data, the Quick Start) starts out writable.
RUN mkdir -p /out/data /out/backups

# distroless/static has CA certificates and a nonroot user, and nothing else
# (no shell). The time zone database is compiled into the binary.
# Pinned by digest; Dependabot proposes updates (.github/dependabot.yml).
FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab
COPY --from=build /out/eumetria /eumetria
COPY --from=build --chown=65532:65532 /out/data /data
COPY --from=build --chown=65532:65532 /out/backups /backups
VOLUME /data /backups
# Every command finds the data folder, also one-off ones that replace CMD
# (docker compose run … -create-user …).
ENV DATA_DIR=/data BACKUP_DIR=/backups
# Below the container's 256 MB (deploy/compose.yaml), so the garbage collector
# works harder before the kernel kills the process. Raise both together.
ENV GOMEMLIMIT=200MiB
EXPOSE 8080
HEALTHCHECK --interval=1m --timeout=10s --start-period=30s \
  CMD ["/eumetria", "-healthcheck", "http://127.0.0.1:8080/healthz"]
ENTRYPOINT ["/eumetria"]
CMD ["-serve", ":8080"]
