# deadzone

**Can I take this video call from an off-grid homestead?**

One Go binary that reads your solar/battery system (Solar Assistant over
MQTT), your Starlink dish (local gRPC) and the weather (Open-Meteo), and
answers three questions every few seconds:

1. **Can I take a video call now?** — GO / MAYBE / NO, with the reasons.
2. **How many hours of workstation do I have at this SOC?** — conservative
   (no sun) and forecast-integrated ("until 02:40"), with an honesty band.
3. **What should I turn off before the call?** — ranked list with the hours
   each load buys you.

It publishes the answer over MQTT with Home Assistant discovery (zero-config
entities), serves a phone-sized status page, and ships as a HA add-on, a
systemd service, or a container. It never writes to your inverter.

```
$ deadzone check
{ "verdict": { "call_ok": "amber",
    "call_reasons": ["uplink:drop_5m=0.031>0.02", "uplink:lat_p90=61ms<=150ms",
                     "power:usable=5222Wh>=call_budget=938Wh", "power:soc_trust=low"],
    "hours_workstation": 7.0, "hours_workstation_with_pv": 11.8, "until": "2026-11-01T05:21:33Z",
    "shed": [{"load":"water_heater","w":1200,"gain_h":3.1}] }, ... }
$ echo $?
1          # 0 = go, 1 = maybe, 2 = no
```

## 60-second install on Home Assistant Yellow (or any HAOS box)

1. Settings → Add-ons → Add-on store → ⋮ → **Repositories** → add
   `https://github.com/rafe-walker/deadzone` → close → find **deadzone** →
   Install.
2. Configuration tab: set `mqtt_host` to your Solar Assistant Pi's IP
   (its broker must be started under SA → Configuration → MQTT), or leave
   it empty if SA bridges into your Mosquitto add-on. Set `latitude`,
   `longitude`, `battery_kwh`. Save.
3. Start. Open the sidebar entry for the status page. Within a minute a
   **deadzone** device appears under Settings → Devices (MQTT) with
   `sensor.deadzone_verdict`, `binary_sensor.deadzone_call_ok`,
   `sensor.deadzone_hours_at_current_load` and friends.

The add-on builds the Go binary on the Yellow the first time (a few
minutes on a CM4). Starlink needs `192.168.100.1` reachable from the HA
box; in bypass mode add a static route on your router.

## Standalone on a Pi / NUC

```sh
# grab the release tarball for your arch (linux-arm64 for Pi 4/5, linux-amd64 for NUC)
tar xzf deadzone-v0.1.0-linux-arm64.tar.gz && cd deadzone-v0.1.0-linux-arm64
sudo install -m 755 deadzone /usr/local/bin/deadzone
sudo mkdir -p /etc/deadzone /var/lib/deadzone
sudo cp deadzone.example.yaml /etc/deadzone/deadzone.yaml   # edit mqtt.host, forecast.latitude/longitude, battery.capacity_kwh
sudo useradd -r -s /usr/sbin/nologin deadzone; sudo chown deadzone /var/lib/deadzone
sudo cp deploy/deadzone.service /etc/systemd/system/ && sudo systemctl enable --now deadzone
curl -s localhost:8099/api/state | jq .verdict
```

Or `docker build -f deploy/Dockerfile -t deadzone .` and run with
`DEADZONE_*` env vars (see below). From source: `go build ./cmd/deadzone`
(Go 1.24, no CGO).

```
deadzone run     [--config FILE] [--interval 5s] [--log-level debug]
deadzone check   [--config FILE] [--wait 15s]      # one-shot JSON, exit 0/1/2
deadzone version
```

## Configuration

YAML file, every key overridable by a `DEADZONE_*` environment variable.
[`deadzone.example.yaml`](deadzone.example.yaml) documents all of them. The ones
you must set or measure:

| Key | Env | What |
| --- | --- | --- |
| `mqtt.host` | `DEADZONE_MQTT_HOST` | Solar Assistant broker (its Pi's IP) or a broker SA bridges into |
| `mqtt.sa_prefix` | `DEADZONE_SA_PREFIX` | `solar_assistant` unless you changed it |
| `starlink.addr` | `DEADZONE_STARLINK_ADDR` | `192.168.100.1:9200` |
| `forecast.latitude` / `longitude` | `DEADZONE_LAT` / `DEADZONE_LON` | required when forecast is enabled |
| `forecast.tilt_deg` / `azimuth_deg` / `array_kwp` | `DEADZONE_TILT_DEG` / `DEADZONE_AZIMUTH_DEG` / `DEADZONE_ARRAY_KWP` | array geometry; azimuth 0 = south, −90 = east, 90 = west |
| `battery.capacity_kwh` | `DEADZONE_BATTERY_KWH` | packs × Ah × 51.2 V (two Eco-Worthy 100 Ah = 10.24) |
| `battery.reserve_soc_pct` | `DEADZONE_RESERVE_SOC` | SOC floor, default 20 |
| `battery.parasitic_w` | `DEADZONE_PARASITIC_W` | inverter idle draw not in SA's load number |
| `workstation.watts` | `DEADZONE_WORKSTATION_W` | laptop + monitor + dish; used when the measured load is lower |
| `workstation.call_length_min` | `DEADZONE_CALL_LENGTH_MIN` | power budget horizon (60) |
| `thresholds.*` | — | uplink cut-offs, stale window (30 s), call margin |
| `shed[]` | — | `{name, watts, priority, control}` loads you could turn off |
| `http.listen` | `DEADZONE_HTTP_LISTEN` | `:8099` |

## The three formulas, in plain words

**Hours at current SOC (Q2).** Usable energy is `capacity × (SOC − reserve)`.
The load is a 10-minute moving average of Solar Assistant's total load, or
your workstation profile if the workstation is evidently off, plus the
inverters' idle draw. Divide energy by load and you get the conservative
number (no sun). For the optimistic number deadzone walks the Open-Meteo
15-minute irradiance forecast, converts it to watts with your array size
and a performance ratio it learns by comparing forecast to the PV you
actually produced, and keeps subtracting until the reserve is hit — that
gives a clock time. If your SOC comes from the inverter's voltage guess
(Solar Assistant "Use inverter values"), the estimate is shown as a band
and labelled low-trust; LiFePO4 voltage is flat between ~30 % and ~90 %
and a BMS read is the single biggest accuracy upgrade.

**Can I take the call (Q1).** Two gates, the verdict is the worse one.
*Uplink*: over the last five minutes of 1-second dish history, mean ping
drop ≤ 2 %, p90 latency ≤ 150 ms, jitter ≤ 30 ms and no outage ≥ 5 s is
green; ≤ 5 %, ≤ 250 ms, ≤ 50 ms and at most one such outage is amber;
worse is red. Dish not CONNECTED, thermal shutdown, or currently
obstructed is red. Dish heating, "lower signal than predicted", or more
than 2 mm of rain forecast in the next two hours caps it at amber
("storm risk"). *Power*: red if SOC is within 5 points of reserve or an
inverter reports a fault; green if usable energy covers
`(workstation + dish) × call length × 1.25`; otherwise amber. If either
side has no data (SA silent for 30 s, dish unreachable) it reads
**unknown** and the published verdict is amber with the reason — deadzone
does not guess.

**What to shed (Q3).** Each configured load is ranked by `watts ÷
priority`. For each one, the hours gained are `energy ÷ (load − watts) −
energy ÷ load`. The list grows until the runtime reaches your target
hours. v0.1 only recommends; your HA automation flips the switches.

## MQTT and Home Assistant

Retained `deadzone/state` carries the whole JSON document (same as
`GET /api/state`). Convenience topics: `deadzone/verdict` (green|amber|red),
`deadzone/hours_at_soc`, `deadzone/shed_list`, `deadzone/verdict/call_reasons`,
`deadzone/verdict/hours_with_pv`, `deadzone/verdict/until`,
`deadzone/power/soc_trust`, `deadzone/uplink/starlink/score`,
`deadzone/forecast/pv_wh_next_6h`, and `deadzone/availability` (LWT).
Discovery configs go to `homeassistant/{sensor,binary_sensor}/deadzone/<key>/config`
and all template the one state topic.

## What it does not do

- It does not control anything. No `/set` writes to Solar Assistant, no
  switch flipping. The `actions` interface exists; live actuation is v0.2
  and opt-in.
- It does not read your BMS. SOC is whatever Solar Assistant publishes. If
  that is the inverter's voltage estimate, deadzone says so and widens the
  band. For the real number read the pack directly —
  [jbd-up](https://github.com/rafe-walker/jbd-up) does that for Eco-Worthy
  / JBD UP16S racks and feeds Solar Assistant or MQTT.
- It does not replace Solar Assistant. SA does the serial protocol work and
  owns the inverter port; deadzone is a consumer of its MQTT output. Direct
  PI30 serial is on the roadmap as a *replacement* source for people
  without SA, not an addition.
- It does not know about cellular failover, EcoFlow units or an active
  probe to the internet yet. See [ROADMAP.md](ROADMAP.md).
- It does not predict Starlink rain fade with any proven lead time. The
  storm cap is a prior from the forecast, not a measurement; the SIGSPATIAL
  '25 terminal study that motivated it found correlation, not a predictor.
- Thresholds are opinions, tuned for WebRTC-style calls. Change them in
  `thresholds.uplink`.

## Development

```sh
go test ./...        # ~60 tests, no network
go vet ./...
make cross           # linux/amd64, linux/arm64, darwin/arm64 into dist/
```

All fixtures are synthetic and labelled as such; a real
`mosquitto_sub -v -t 'solar_assistant/#'` dump from your bench makes a
better one — please contribute it (scrubbed). See
[CONTRIBUTING.md](CONTRIBUTING.md).

## License

Apache-2.0. Starlink is a trademark of SpaceX; Solar Assistant is a
trademark of its owners; deadzone is affiliated with neither.

---

Built off-grid in Cochise County, AZ by a laid-off support engineer —
https://theexiledentrepreneur.com
