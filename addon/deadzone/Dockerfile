# Stage 1: build the static Go binary from the pinned release tag.
# The Supervisor builds with addon/deadzone/ as the context, so the source
# is fetched from GitHub rather than copied. Bump DEADZONE_REF together with
# config.yaml "version".
FROM golang:1.24-alpine AS builder
ARG DEADZONE_REF=main
ARG BUILD_ARCH=aarch64
RUN apk add --no-cache git ca-certificates
WORKDIR /src
RUN git clone --depth 1 --branch "${DEADZONE_REF}" https://github.com/rafe-walker/deadzone.git . \
 || (git clone https://github.com/rafe-walker/deadzone.git . && git checkout "${DEADZONE_REF}")
RUN case "${BUILD_ARCH}" in aarch64) GOARCH=arm64 ;; amd64) GOARCH=amd64 ;; armv7) GOARCH=arm ;; *) GOARCH="" ;; esac \
 && CGO_ENABLED=0 GOOS=linux GOARCH="${GOARCH}" \
    go build -trimpath -ldflags="-s -w -X main.version=${DEADZONE_REF}" -o /out/deadzone ./cmd/deadzone

# Stage 2: the HA base image (bashio + s6) with just the binary.
ARG BUILD_FROM=ghcr.io/home-assistant/aarch64-base:3.20
FROM ${BUILD_FROM}
RUN apk add --no-cache jq ca-certificates
COPY --from=builder /out/deadzone /usr/bin/deadzone
COPY run.sh /run.sh
RUN chmod a+x /run.sh
CMD ["/run.sh"]
