# 3. Solar position algorithms and default conventions

- Status: accepted
- Date: 2026-11-01

## Context

Every later stage of the pipeline (angle of incidence, air mass, the Perez
model) depends on the solar position, so its accuracy bounds the accuracy of
the whole forecast. The SPA also needs inputs that users rarely know: ΔT and
the annual mean pressure and temperature used for refraction.

## Decision

- **SPA is the default algorithm.** It is the published reference
  (±0.0003°) and it matches pvlib `spa_python` to the precision of the
  stored fixtures (about 1e-10°).
- **NOAA is offered as a fast alternative** behind the same `Algorithm`
  interface. It is about 9× faster and stays within 0.015° of SPA for
  daytime samples, which is far below the uncertainty of any weather
  forecast. Its azimuth uses the atan2 form instead of the calculator's
  acos form, which is mathematically equivalent but stays well conditioned
  near the zenith.
- **ΔT defaults to 67 s**, the pvlib default, so that results are directly
  comparable. ΔT enters only the ephemeris; the 2–3 s difference to the
  current observed value changes the angles by far less than 0.001°.
- **Refraction defaults to the ICAO standard-atmosphere pressure at the site
  altitude and 12 °C.** A fixed sea-level pressure would overstate
  refraction at high-altitude sites.
- **Extraterrestrial irradiance uses a solar constant of 1361 W/m²** (IAU
  2015 Resolution B3) scaled by the SPA Earth–Sun distance, rather than the
  older 1366.1 W/m² that pvlib uses by default. Fixtures that depend on it
  pass the value explicitly.
- **Times are instants.** The algorithms take a `time.Time` and use only the
  instant it denotes; the Julian day is derived from Unix seconds, which is
  valid over the whole SPA range (-2000 to 6000).

## Consequences

Results match pvlib by default, apart from the solar constant, which is
documented. Users who need refraction for real local conditions can set
`Site.Atmosphere`.
