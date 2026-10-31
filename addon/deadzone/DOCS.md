# deadzone add-on

Answers one question on your Home Assistant dashboard: **can I take this
video call right now?** It reads your solar/battery system through Solar
Assistant's MQTT broker, your Starlink dish through its local gRPC port,
and the weather through Open-Meteo, and publishes a GO / MAYBE / NO
verdict plus hours-of-runtime and a "turn these off first" list. All of it
appears in Home Assistant automatically through MQTT discovery.

## Before you install

1. **Solar Assistant** → Configuration → MQTT: start the broker. Note the
   Pi's IP. (Or bridge SA into the Mosquitto add-on; then leave `mqtt_host`
   empty and deadzone uses the Supervisor's MQTT service.)
2. **Starlink**: the dish must be reachable at `192.168.100.1:9200` from
   the HA box. In bypass mode add a static route for 192.168.100.1 on your
   router. If it is not reachable the verdict degrades to MAYBE with
   "starlink_unknown" — it never pretends.
3. **Mosquitto add-on + MQTT integration** installed in HA so discovery
   entities show up.

## Options

| Option | Meaning |
| --- | --- |
| `mqtt_host` / `mqtt_port` / `mqtt_user` / `mqtt_password` | Broker to subscribe to Solar Assistant on and publish `deadzone/*` to. Empty host = Supervisor MQTT service. Use an IP, not `.local`. |
| `sa_prefix` | Solar Assistant topic prefix (`solar_assistant`). |
| `battery_positive_is_charging` | Sign of `total/battery_power` for your inverter driver. Flip if the battery reads "charging" at night. |
| `starlink_enabled` / `starlink_addr` / `starlink_dish_watts` | Dish gRPC endpoint; watts used in the call budget when the dish does not report power. |
| `forecast_enabled` / `latitude` / `longitude` / `tilt_deg` / `azimuth_deg` / `array_kwp` | Open-Meteo GTI + rain. Azimuth: 0 = south, -90 = east, 90 = west. `array_kwp` 0 disables PV watts but keeps the rain/cloud signal. |
| `battery_kwh` | Nominal pack energy: packs × Ah × 51.2 V. Two Eco-Worthy 100 Ah racks = 10.24. |
| `reserve_soc_pct` | SOC floor you will not cross. |
| `parasitic_w` | Inverter idle draw not included in Solar Assistant's load. |
| `workstation_watts` | Laptop + monitor + dish. Used when measured load is lower (workstation off). |
| `call_length_min` | Power budget horizon for the verdict. |
| `target_hours` | Shed list stops once runtime reaches this. |
| `stale_after_s` | No SA message for this long → power side reads unknown. |
| `shed` | List of `{name, watts, priority, control}`. Priority 1 is shed first. `control` is free text for your automation. |
| `log_level` | debug / info / warn / error. |

## What you get in Home Assistant

Device **deadzone** with sensors `deadzone_verdict` (green/amber/red),
`deadzone_hours_at_current_load`, `deadzone_hours_with_pv`,
`deadzone_battery_soc`, `deadzone_pv_w`, `deadzone_load_w`,
`deadzone_starlink_pop_ping_ms`, `deadzone_starlink_downlink_mbps`,
`deadzone_forecast_cloud_pct`, `deadzone_uplink_score`,
`deadzone_shed_recommendation`, and binary sensors `deadzone_call_ok`,
`deadzone_starlink_obstructed`. Everything is templated from the retained
`deadzone/state` JSON, so a single MQTT sensor on that topic exposes any
other field you want.

The status page is in the sidebar (ingress). Optionally expose port 8099
for `GET /api/state` from scripts.

## Example automation

```yaml
alias: Shed before a call
trigger:
  - platform: state
    entity_id: sensor.deadzone_verdict
    to: amber
condition:
  - condition: template
    value_template: "{{ 'water_heater' in states('sensor.deadzone_shed_recommendation') }}"
action:
  - service: switch.turn_off
    target: { entity_id: switch.water_heater }
  - service: notify.mobile_app_phone
    data:
      message: "Call verdict is MAYBE: water heater off, {{ states('sensor.deadzone_hours_at_current_load') }} h left."
```

## Troubleshooting

- **Everything unknown**: check `mqtt_host` is the Solar Assistant IP and
  the SA broker is started. Log shows `mqtt connected` then `subscribed`.
- **Starlink unknown with "reflection"** in the error: the dish firmware
  changed its reflection output. deadzone already tolerates missing
  descriptor files; open an issue with the log line.
- **Hours look wrong**: SOC on a voltage-only setup ("Use inverter values")
  is a guess; the band in the status page shows how wide. A BMS read or
  SA's "Emulated BMS" tightens it.
