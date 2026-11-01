# pvforecast-go

[![CI](https://github.com/kviatkovsky/pvforecast-go/actions/workflows/ci.yml/badge.svg)](https://github.com/kviatkovsky/pvforecast-go/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/kviatkovsky/pvforecast-go.svg)](https://pkg.go.dev/github.com/kviatkovsky/pvforecast-go)
[![Go Report Card](https://goreportcard.com/badge/github.com/kviatkovsky/pvforecast-go)](https://goreportcard.com/report/github.com/kviatkovsky/pvforecast-go)
[![OpenSSF Scorecard](https://api.scorecard.dev/projects/github.com/kviatkovsky/pvforecast-go/badge)](https://scorecard.dev/viewer/?uri=github.com/kviatkovsky/pvforecast-go)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

Physics-based photovoltaic power forecasting in Go. Give it a PV system
(location, kWp, tilt, azimuth, losses) and it fetches a weather forecast and
returns hourly AC power and daily energy for the next 1–7 days.

Every model is a published, textbook method (NREL SPA, Perez, PVWatts v5),
validated against [pvlib python](https://github.com/pvlib/pvlib-python). The
library has no dependencies outside the Go standard library.

> **Status:** under active development towards v0.1.0. The API is not stable
> yet.

## Roadmap to v0.1.0

- [x] Repository skeleton, CI, lint and supply-chain tooling
- [ ] `solarpos`: NREL SPA and NOAA solar position
- [ ] `irradiance`: angle of incidence, isotropic, Hay-Davies, Perez, Erbs
- [ ] `thermal`: Faiman and SAPM cell temperature
- [ ] `power`: PVWatts v5 DC, losses, inverter and clipping
- [ ] `weather/openmeteo`: Open-Meteo forecast provider
- [ ] `forecast`: pipeline, multi-array systems, interval conventions
- [ ] `pvforecast` CLI with table, JSON and CSV output
- [ ] Validation against PVGIS and Open-Meteo GTI
- [ ] Signed release binaries

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). Design decisions are recorded in
[docs/adr](docs/adr).

## License

[MIT](LICENSE)
