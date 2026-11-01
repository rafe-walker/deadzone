# Changelog

## 0.2.0

- Starlink: dish power is read from `get_history.power_in` (5-minute mean,
  newest sample and min/max published; the call budget uses the mean and
  says which source it used, `dish_w=62(dish_mean_5m)`); the configured
  `starlink_dish_watts` is only the fallback.
- Starlink: `get_diagnostics` polled once a minute. `disablement_code`
  other than OKAY (NO_ACTIVE_ACCOUNT, DATA_OVERAGE_SANDBOX_POLICY, ...),
  `stowed` and `overage_rate_limited` are red uplink conditions with
  reasons; `dl/ul_bandwidth_restricted_reason=OVERAGE_LIMIT` from
  get_status is red too, other policy limits amber. Dishes whose firmware
  rejects get_diagnostics keep working without it.
- Two new entities: `sensor.deadzone_starlink_power_w` and the diagnostic
  `sensor.deadzone_starlink_disablement_code`.
- Status page: per-source data-age chips (SA / dish / forecast), a
  15-minute drop/p90 sparkline from the dish history (`history_15m` on
  `/api/state`, 5-s buckets), a "next 6 h" green/amber/red strip from the
  forecast and the reserve time, and a web manifest so it installs to a
  phone home screen.
- Store: icon, logo and English labels for every option on the
  Configuration tab.
- Blueprint `blueprints/automation/deadzone/call_ok_notify.yaml` (notify
  when the call verdict flips) and a GHCR image workflow; the add-on still
  builds on the device by default, see README "Maintainer: publishing an
  image".

## 0.1.1

- Fix: a NaN in the fused state broke every MQTT publish (`json: unsupported value: NaN`). Non-finite values are now nulled and the field is logged.
- Fix: Dockerfile ARG scope so the Supervisor build works.


## 0.1.0

- First release. Solar Assistant MQTT + Starlink gRPC (reflection) +
  Open-Meteo; verdict, hours-at-SOC, shed list; MQTT publish with HA
  discovery; ingress status page.
