# syntax=docker/dockerfile:1

# ---- Stage 1: build the SPA -------------------------------------------------
FROM node:lts-alpine AS web
WORKDIR /web
COPY web/package.json web/package-lock.json* ./
RUN npm ci
COPY web/ ./
RUN npm run build

# ---- Stage 2: build the static Go binary ------------------------------------
FROM golang:1.26-alpine AS build
ARG VERSION=dev
ARG COMMIT=none
ARG DATE=unknown
WORKDIR /src
# Pre-fetch modules for better layer caching.
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# Bring in the freshly built SPA so go:embed picks up real assets.
COPY --from=web /web/dist ./web/dist
# Create a /data skeleton owned by the non-root runtime user.
RUN mkdir -p /out/data && \
    CGO_ENABLED=0 GOFLAGS=-mod=mod go build -trimpath \
      -ldflags="-w -s -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.date=${DATE}" \
      -o /out/gotifacts ./cmd/gotifacts

# ---- Stage 3: minimal runtime image -----------------------------------------
FROM scratch AS runtime
# CA certificates for any outbound TLS (pattern; service itself never serves TLS).
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/gotifacts /gotifacts
# Writable data directory owned by the non-root numeric UID.
COPY --from=build --chown=65532:65532 /out/data /data

USER 65532:65532
EXPOSE 8080
VOLUME ["/data"]
ENTRYPOINT ["/gotifacts"]
CMD ["serve"]
