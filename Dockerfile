# syntax=docker/dockerfile:1.7

FROM node:24-bookworm-slim AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN --mount=type=cache,target=/root/.npm npm ci
COPY web/ ./
RUN npm run build

FROM golang:1.26-bookworm AS backend
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY cmd/ ./cmd/
COPY internal/ ./internal/
ARG VERSION=dev
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    version="${VERSION#v}" && \
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
      -trimpath -buildvcs=false \
      -ldflags "-s -w -buildid= -X github.com/Relayward/relayward/internal/buildinfo.Version=${version}" \
      -o /out/relayward ./cmd/relayward

FROM debian:bookworm-slim
ARG VERSION=dev
LABEL org.opencontainers.image.source="https://github.com/Relayward/relayward" \
      org.opencontainers.image.description="Relayward control plane" \
      org.opencontainers.image.version="${VERSION}"
RUN apt-get update && \
    apt-get install -y --no-install-recommends ca-certificates && \
    rm -rf /var/lib/apt/lists/* && \
    groupadd --gid 10001 relayward && \
    useradd --uid 10001 --gid relayward --home-dir /var/lib/relayward --shell /usr/sbin/nologin relayward && \
    install -d -o relayward -g relayward -m 0700 /var/lib/relayward && \
    install -d -o root -g root -m 0755 /usr/share/relayward/web
COPY --from=backend --chmod=0755 /out/relayward /usr/local/bin/relayward
COPY --from=web /src/web/dist/ /usr/share/relayward/web/
USER 10001:10001
EXPOSE 8080
VOLUME ["/var/lib/relayward"]
ENTRYPOINT ["/usr/local/bin/relayward"]
CMD ["serve", "-listen", "0.0.0.0:8080", "-data", "/var/lib/relayward", "-web", "/usr/share/relayward/web"]
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 CMD ["/usr/local/bin/relayward", "healthcheck"]
