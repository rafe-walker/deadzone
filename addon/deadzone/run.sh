#!/usr/bin/with-contenv bashio
# shellcheck shell=bash
set -euo pipefail

OPTIONS=/data/options.json
CONFIG=/data/deadzone.yaml
CACHE=/data/forecast.json

MQTT_HOST=$(jq -r '.mqtt_host // empty' "$OPTIONS")
MQTT_PORT=$(jq -r '.mqtt_port // 1883' "$OPTIONS")
MQTT_USER=$(jq -r '.mqtt_user // empty' "$OPTIONS")
MQTT_PASSWORD=$(jq -r '.mqtt_password // empty' "$OPTIONS")

# Empty host -> use the Mosquitto add-on the Supervisor knows about. If
# Solar Assistant's own broker is what you want, set mqtt_host to the SA
# Pi's IP (mDNS .local names may not resolve inside the container).
if [ -z "$MQTT_HOST" ] && bashio::services.available mqtt; then
  MQTT_HOST=$(bashio::services mqtt "host")
  MQTT_PORT=$(bashio::services mqtt "port")
  MQTT_USER=$(bashio::services mqtt "username")
  MQTT_PASSWORD=$(bashio::services mqtt "password")
  bashio::log.info "Using Supervisor MQTT service at ${MQTT_HOST}:${MQTT_PORT}"
fi
if [ -z "$MQTT_HOST" ]; then
  bashio::log.fatal "mqtt_host is empty and no MQTT service is available. Set mqtt_host to your Solar Assistant IP."
  exit 1
fi

# options.json -> deadzone.yaml (JSON is valid YAML; the daemon reads it).
jq --arg host "$MQTT_HOST" --argjson port "$MQTT_PORT" --arg user "$MQTT_USER" --arg pass "$MQTT_PASSWORD" --arg cache "$CACHE" '{
  log_level: .log_level,
  mqtt: {
    host: $host, port: $port, user: $user, pass: $pass,
    sa_prefix: .sa_prefix, battery_positive_is_charging: .battery_positive_is_charging,
    ha_discovery: true
  },
  starlink: { enabled: .starlink_enabled, addr: .starlink_addr, dish_watts: .starlink_dish_watts },
  forecast: {
    enabled: .forecast_enabled, latitude: .latitude, longitude: .longitude,
    tilt_deg: .tilt_deg, azimuth_deg: .azimuth_deg, array_kwp: .array_kwp, cache_path: $cache
  },
  battery: { capacity_kwh: .battery_kwh, reserve_soc_pct: .reserve_soc_pct, parasitic_w: .parasitic_w },
  workstation: { watts: .workstation_watts, call_length_min: .call_length_min, target_hours: .target_hours },
  thresholds: { stale_after_s: .stale_after_s },
  shed: [ (.shed // [])[] | { name: .name, watts: .watts, priority: (.priority // 1), control: (.control // "") } ],
  http: { listen: ":8099" }
}' "$OPTIONS" > "$CONFIG"

bashio::log.info "deadzone starting (broker ${MQTT_HOST}:${MQTT_PORT}, config ${CONFIG})"
exec /usr/bin/deadzone run --config "$CONFIG"
