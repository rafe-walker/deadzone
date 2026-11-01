# deadzone

Answers one question on your Home Assistant dashboard: **can I take this
video call right now?** It reads your solar/battery system through Solar
Assistant's MQTT broker, your Starlink dish through its local gRPC port,
and the weather through Open-Meteo, and publishes a GO / MAYBE / NO
verdict plus hours-of-runtime and a "turn these off first" list. All of it
appears in Home Assistant automatically through MQTT discovery.

## Installation

1. **Settings → Apps → App store → ⋮ (top right) → Repositories**, add
   `https://github.com/rafe-walker/deadzone`, close the dialog.
2. Find **deadzone** in the store and press **Install**. The app builds the
   Go binary on your box the first time (a few minutes on a Yellow / CM4;
   under a minute on x86).
3. Open the **Configuration** tab, set *MQTT host*, *Latitude*,
   *Longitude* and *Battery capacity*, save.
4. **Start**. The status page is in the sidebar; within a minute a
   **deadzone** device appears under Settings → Devices & services → MQTT.

## Before you install

1. **Solar Assistant** → Configuration → MQTT: start the broker. Note the
   Pi's IP. (Or bridge SA into the Mosquitto app; then leave *MQTT host*
   empty and deadzone uses the Supervisor's MQTT service.)
2. **Starlink**: the dish must be reachable at `192.168.100.1:9200` from
   the HA box. In bypass mode add a static route for 192.168.100.1 on your
   router. If it is not reachable the verdict degrades to MAYBE with
   "starlink_unknown" — it never pretends. Local gRPC location
   (`get_location`) is off on most residential plans and deadzone never
   asks for it; the forecast uses the latitude/longitude you configure.
3. **Mosquitto app + MQTT integration** installed in HA so discovery
   entities show up.

## Options

Every option has a label and a description on the Configuration tab. The
ones that matter most:

| Option | Meaning |
| --- | --- |
| `mqtt_host` / `mqtt_port` / `mqtt_user` / `mqtt_password` | Broker to subscribe to Solar Assistant on and publish `deadzone/*` to. Empty host = Supervisor MQTT service. Use an IP, not `.local`. |
| `sa_prefix` | Solar Assistant topic prefix (`solar_assistant`). |
| `battery_positive_is_charging` | Sign of `total/battery_power` for your inverter driver. Flip if the battery reads "charging" at night. |
| `starlink_enabled` / `starlink_addr` / `starlink_dish_watts` | Dish gRPC endpoint. The watts are a **fallback only**: when the dish reports `power_in` in `get_history` (every current dish does) the 5-minute mean is used instead. |
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

Example `shed` entry (Configuration tab → YAML mode):

```yaml
shed:
  - name: water_heater
    watts: 1200
    priority: 1
    control: switch.water_heater
  - name: well_pump
    watts: 800
    priority: 3
```

## What you get in Home Assistant

Device **deadzone** with sensors `deadzone_verdict` (green/amber/red),
`deadzone_hours_at_current_load`, `deadzone_hours_with_pv`,
`deadzone_battery_soc`, `deadzone_pv_w`, `deadzone_load_w`,
`deadzone_starlink_pop_ping_ms`, `deadzone_starlink_downlink_mbps`,
`deadzone_starlink_power_w`, `deadzone_forecast_cloud_pct`,
`deadzone_uplink_score`, `deadzone_starlink_drop_5m`,
`deadzone_starlink_disablement_code`, `deadzone_shed_recommendation`, and
binary sensors `deadzone_call_ok`, `deadzone_starlink_obstructed`.
Everything is templated from the retained `deadzone/state` JSON, so a
single MQTT sensor on that topic exposes any other field you want.

The status page is in the sidebar (ingress). It shows per-source data-age
chips, a 15-minute dish drop/latency sparkline and a "next 6 h" strip.
Optionally expose port 8099 for `GET /api/state` from scripts and to
install the page on a phone home screen (open `http://<ha>:8099/` in the
phone browser → "Add to Home Screen"; the ingress URL also works but
its session expires).

## Why the verdict went red: Starlink account and policy states

A dish with a lapsed subscription or a data overage keeps answering pings
and keeps saying CONNECTED, so ping statistics alone would say GO.
deadzone reads `disablement_code` (from `get_status` and
`get_diagnostics`) and treats anything but OKAY as red with the code in
the reasons (`NO_ACTIVE_ACCOUNT`, `DATA_OVERAGE_SANDBOX_POLICY`,
`TOO_FAR_FROM_SERVICE_ADDRESS`, `ROAM_RESTRICTED`, ...). `stowed`,
`overage_rate_limited` and an `OVERAGE_LIMIT` rate-limit reason are red
too; `POLICY_LIMIT` / `LOW_SPEED_POLICY_LIMIT` cap at amber. Firmware
that does not know `get_diagnostics` logs one warning and carries on with
status + history.

## Automations

A ready-made blueprint notifies you when `binary_sensor.deadzone_call_ok`
flips — import it from the repository's `blueprints/automation/deadzone/`
folder (README has the one-click link). Example of a plain automation
that sheds a load when the verdict turns red:

```yaml
alias: Shed water heater when the call verdict goes red
triggers:
  - trigger: state
    entity_id: sensor.deadzone_verdict
    to: red
conditions:
  - condition: template
    value_template: "{{ 'water_heater' in states('sensor.deadzone_shed_recommendation') }}"
actions:
  - action: switch.turn_off
    target: { entity_id: switch.water_heater }
  - action: notify.mobile_app_phone
    data:
      message: "Call verdict is NO: water heater off, {{ states('sensor.deadzone_hours_at_current_load') }} h left."
```

## Troubleshooting

- **Everything unknown**: check *MQTT host* is the Solar Assistant IP and
  the SA broker is started. Log shows `mqtt connected` then `subscribed`.
- **Starlink unknown with "reflection"** in the error: the dish firmware
  changed its reflection output. deadzone already tolerates missing
  descriptor files; open an issue with the log line.
- **"get_diagnostics not supported"** in the log: old dish firmware. The
  disablement / stow checks are off; everything else works.
- **Hours look wrong**: SOC on a voltage-only setup ("Use inverter values")
  is a guess; the band in the status page shows how wide. A BMS read or
  SA's "Emulated BMS" tightens it.
- **Configuration tab shows raw keys**: your HA is older than the
  translations support; the keys are documented above.

## Support

Issues and feature requests: https://github.com/rafe-walker/deadzone/issues.
Apache-2.0. Starlink is a trademark of SpaceX; Solar Assistant is a
trademark of its owners; deadzone is affiliated with neither.
