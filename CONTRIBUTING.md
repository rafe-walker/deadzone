# Contributing to deadzone

Thanks for helping. deadzone is a small daemon with a sharp scope: fuse
power telemetry and uplink quality into three answers for off-grid remote
workers. Contributions that keep it small, honest about uncertainty, and
hardware-agnostic are the ones most likely to land.

## Ground rules

- **Read-only by default.** Anything that writes to an inverter, a BMS, a
  router, or a smart plug lives behind the `actions` interface, is off unless
  `actions.enabled: true` *and* the operator passes `--allow-actions`, and must
  have a dry-run implementation.
- **No real credentials, coordinates, or private IPs** in code, tests,
  fixtures, or docs. The only hard-coded address allowed is the Starlink
  default `192.168.100.1`.
- **Fixtures are labelled.** If you record a fixture from real hardware,
  scrub it and say what it came from. If you construct one by hand, mark it
  `synthetic` in the file.
- **Formulas are tested.** Changes to `internal/engine` need table-driven
  tests. If you change a default threshold, update `deadzone.example.yaml`
  and the README config reference in the same PR.
- **Degrade gracefully.** New sources must cope with missing fields, stale
  data, and the uplink being down (that is, after all, the point).

## Development

```sh
go build ./...
go vet ./...
go test ./...
golangci-lint run      # optional, not a hard gate
make cross             # linux/amd64, linux/arm64, darwin/arm64
```

Run against a live broker:

```sh
go run ./cmd/deadzone --config deadzone.yaml --log-level debug
```

## Developer Certificate of Origin

This project uses the [Developer Certificate of Origin](https://developercertificate.org/)
(DCO) instead of a CLA. By contributing you certify that:

```
Developer Certificate of Origin
Version 1.1

Copyright (C) 2004, 2006 The Linux Foundation and its contributors.

Everyone is permitted to copy and distribute verbatim copies of this
license document, but changing it is not allowed.

Developer's Certificate of Origin 1.1

By making a contribution to this project, I certify that:

(a) The contribution was created in whole or in part by me and I
    have the right to submit it under the open source license
    indicated in the file; or

(b) The contribution is based upon previous work that, to the best
    of my knowledge, is covered under an appropriate open source
    license and I have the right under that license to submit that
    work with modifications, whether created in whole or in part
    by me, under the same open source license (unless I am
    permitted to submit under a different license), as indicated
    in the file; or

(c) The contribution was provided directly to me by some other
    person who certified (a), (b) or (c) and I have not modified
    it.

(d) I understand and agree that this project and the contribution
    are public and that a record of the contribution (including all
    personal information I submit with it, including my sign-off) is
    maintained indefinitely and may be redistributed consistent with
    this project or the open source license(s) involved.
```

Sign off every commit with `git commit -s`, which appends:

```
Signed-off-by: Your Name <you@example.com>
```

Use your real name. Unsigned commits will be asked to be amended.

## Licence

By contributing you agree that your contributions are licensed under the
Apache License 2.0 (see `LICENSE`).
