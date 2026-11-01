# Roadmap

v0.1 was Solar Assistant MQTT + Starlink gRPC + Open-Meteo, advisory only.
v0.2.0 added real dish power, `get_diagnostics` (account / overage / stow
reds), the status-page sparkline, data-age chips, the next-6 h strip, a
web manifest, the notify blueprint, store assets + translations and the
GHCR image workflow (on-device build still the default). Everything below
is scoped in the design doc but deliberately absent from the binary; each
row names the interface it will plug into so nothing has to change shape.

| Module | Plugs into | Status | One-line note |
| --- | --- | --- | --- |
| `source/probe` — ICMP/UDP RTT, jitter, loss to 2–3 anchors; route/public-IP change | `source.Source` → `model.Probe` (already in the state JSON, `enabled: false`) | v0.2 | Router-agnostic failover detection; needs raw-socket capability in the add-on. |
| `source/cellular/{peplink,teltonika,mmcli}` | `source.Source` → `model.Uplink.Cellular` | v0.2 | Per-router drivers; Peplink Router API, Teltonika JSON-RPC, ModemManager `mmcli -J`. |
| `source/pi30` — direct serial `QPIGS`/`QPGSn` | `source.Source` → `model.Power` (replaces `solarassistant`, same port) | v0.2 | For benches without Solar Assistant; either/or with SA on the RS232 port. |
| `source/jbd_up` — Eco-Worthy UP16S BMS | `source.Source` → `model.Pack` | v0.2 | Consume [jbd-up](https://github.com/rafe-walker/jbd-up) MQTT output first; native Go reader later. |
| `source/ecoflow` — Delta Pro via HA entities / developer API | `source.Source` → second `model.Power` | v0.2 | Cloud-only; never gates Q1. |
| `actions/{solarassistant,homeassistant,webhook}` — live shedding | `actions.Actor` (v0.1 ships `Advisory`) | v0.2 | Opt-in: `actions.enabled` + `--allow-actions`; SA `/set` writes serialized with response wait. |
| HA blueprint for shed + notify | `blueprints/automation/deadzone/` | notify shipped 0.2.0; shed blueprint next | Shed stays an example automation until `actions/homeassistant` exists, so the switch-flipping never lives in two places. |
| Forecast.solar cross-check | `source/forecast` | later | Optional second opinion on PV. |
| SSE stream on `/api/stream` | `internal/api` | later | Dashboard polls every 5 s today; fine for one viewer. |
| Pre-built app images on GHCR | `addon/deadzone/config.yaml: image:` | workflow shipped 0.2.0; flip `image:` after the GHCR-public step | Skips the on-device Go build. README "Maintainer: publishing an image" has the ordered procedure. |
| `build.yaml` → Dockerfile-only | `addon/deadzone/` | after the image flip | HA's info helper now warns build.yaml is deprecated; once the image is published the per-arch base list moves into the Dockerfile. |
| Dish night sleep as a shed item | `actions` + `set_sleep_config` over gRPC | v0.3 | The one dish write worth doing; opt-in like every actor. |
