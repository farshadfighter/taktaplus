FROM node:22-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/web/dist/app ./web/dist/app
ARG VERSION=dev
ARG COMMIT=unknown
ARG DATE=unknown
RUN LDFLAGS="-s -w -X github.com/farshadfighter/taktaplus/internal/buildinfo.Version=${VERSION} \
      -X github.com/farshadfighter/taktaplus/internal/buildinfo.Commit=${COMMIT} \
      -X github.com/farshadfighter/taktaplus/internal/buildinfo.Date=${DATE}" && \
    CGO_ENABLED=0 go build -trimpath -ldflags "$LDFLAGS" -o /out/taktaplus ./cmd/taktaplus && \
    mkdir -p /data-skel

# Alpine rather than distroless: PDF reports are printed with headless
# Chromium, which needs its shared libraries and fonts.
FROM alpine:3.20
RUN apk add --no-cache chromium ca-certificates && \
    addgroup -S -g 65532 nonroot && adduser -S -D -H -u 65532 -G nonroot nonroot
COPY --from=build /out/ /usr/local/bin/
# Owned by the "nonroot" user so a fresh named volume is writable.
COPY --from=build --chown=65532:65532 /data-skel /var/lib/taktaplus
USER 65532:65532
VOLUME /var/lib/taktaplus
EXPOSE 8443
ENTRYPOINT ["/usr/local/bin/taktaplus"]
CMD ["api"]
