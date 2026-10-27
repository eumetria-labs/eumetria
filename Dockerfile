# Self-hosted dashboard: one container serves the site and syncs on a schedule.
# See the README, "Setup on a server".

FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
# The page files are compiled into the binary (site/embed.go names them;
# stats.json, photos, routes and days are never included).
COPY site ./site
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/eumetria ./cmd/eumetria

# distroless/static has CA certificates and a nonroot user, and nothing else
# (no shell). The time zone database is compiled into the binary.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/eumetria /eumetria
VOLUME /data
EXPOSE 8080
HEALTHCHECK --interval=1m --timeout=10s --start-period=30s \
  CMD ["/eumetria", "-healthcheck", "http://127.0.0.1:8080/healthz"]
ENTRYPOINT ["/eumetria"]
CMD ["-serve", ":8080", "-data", "/data"]
