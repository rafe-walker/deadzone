# Changelog

## 0.1.1

- Fix: a NaN in the fused state broke every MQTT publish (`json: unsupported value: NaN`). Non-finite values are now nulled and the field is logged.
- Fix: Dockerfile ARG scope so the Supervisor build works.


## 0.1.0

- First release. Solar Assistant MQTT + Starlink gRPC (reflection) +
  Open-Meteo; verdict, hours-at-SOC, shed list; MQTT publish with HA
  discovery; ingress status page.
