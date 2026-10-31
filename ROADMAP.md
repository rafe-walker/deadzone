# Roadmap

v0.1 is Solar Assistant MQTT + Starlink gRPC + Open-Meteo, advisory only.
Everything below is scoped in the design doc but deliberately absent from
the binary; each row names the interface it will plug into so nothing in
v0.1 has to change shape.

| Module | Plugs into | Status | One-line note |
| --- | --- | --- | --- |
| `source/probe` — ICMP/UDP RTT, jitter, loss to 2–3 anchors; route/public-IP change | `source.Source` → `model.Probe` (already in the state JSON, `enabled: false`) | v0.2 | Router-agnostic failover detection; needs raw-socket capability in the add-on. |
| `source/cellular/{peplink,teltonika,mmcli}` | `source.Source` → `model.Uplink.Cellular` | v0.2 | Per-router drivers; Peplink Router API, Teltonika JSON-RPC, ModemManager `mmcli -J`. |
| `source/pi30` — direct serial `QPIGS`/`QPGSn` | `source.Source` → `model.Power` (replaces `solarassistant`, same port) | v0.2 | For benches without Solar Assistant; either/or with SA on the RS232 port. |
| `source/jbd_up` — Eco-Worthy UP16S BMS | `source.Source` → `model.Pack` | v0.2 | Consume [jbd-up](https://github.com/rafe-walker/jbd-up) MQTT output first; native Go reader later. |
| `source/ecoflow` — Delta Pro via HA entities / developer API | `source.Source` → second `model.Power` | v0.2 | Cloud-only; never gates Q1. |
| `actions/{solarassistant,homeassistant,webhook}` — live shedding | `actions.Actor` (v0.1 ships `Advisory`) | v0.2 | Opt-in: `actions.enabled` + `--allow-actions`; SA `/set` writes serialized with response wait. |
| HA blueprint for shed + notify | docs/blueprints | v0.1.x | Answers "why not an HA automation": the numbers come from deadzone. |
| Forecast.solar cross-check | `source/forecast` | later | Optional second opinion on PV. |
| SSE stream on `/api/stream` | `internal/api` | later | Dashboard polls every 5 s today; fine for one viewer. |
| Pre-built add-on images on GHCR | `addon/deadzone/config.yaml: image:` | v0.1.x | Skips the on-device Go build. |
