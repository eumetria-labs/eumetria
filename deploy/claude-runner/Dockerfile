# The Claude Code runner (phase 12b, optional): runs the unmodified Claude
# Code CLI on the owner's subscription for Eumetria's coach and weekly
# review. Built from the repository root:
#   docker build -f deploy/claude-runner/Dockerfile .
# See the README, "Claude Code".

FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY site ./site
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/claude-runner ./cmd/claude-runner

# Claude Code needs Node. Its version is pinned here and raised on purpose:
# the runner parses its stream-json output (internal/ai/claudecode.go).
FROM node:22-bookworm-slim
ARG CLAUDE_CODE_VERSION=2.1.329
RUN npm install -g --no-fund --no-audit @anthropic-ai/claude-code@${CLAUDE_CODE_VERSION} \
 && npm cache clean --force
COPY --from=build /out/claude-runner /usr/local/bin/claude-runner
# The node image's unprivileged user; every job gets an empty home in /tmp.
USER node
ENV HOME=/tmp DISABLE_AUTOUPDATER=1 CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1
EXPOSE 8080
ENTRYPOINT ["claude-runner"]
