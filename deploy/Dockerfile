# Standalone container (not the HA add-on). Build from the repo root:
#   docker build -f deploy/Dockerfile -t deadzone .
#   docker run -d --name deadzone -p 8099:8099 -v deadzone-data:/var/lib/deadzone \
#     -e DEADZONE_MQTT_HOST=192.168.4.70 -e DEADZONE_LAT=31.9 -e DEADZONE_LON=-109.8 \
#     -e DEADZONE_BATTERY_KWH=10.24 deadzone
FROM --platform=$BUILDPLATFORM golang:1.24-alpine AS builder
ARG TARGETOS=linux
ARG TARGETARCH
ARG VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/deadzone ./cmd/deadzone

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=builder /out/deadzone /deadzone
ENV DEADZONE_FORECAST_CACHE=/var/lib/deadzone/forecast.json
VOLUME /var/lib/deadzone
EXPOSE 8099
ENTRYPOINT ["/deadzone"]
CMD ["run"]
