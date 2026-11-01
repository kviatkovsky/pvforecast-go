# 5. Cell temperature models and wind height

- Status: accepted
- Date: 2026-11-01

## Context

Cell temperature changes DC power by about 0.4 % per kelvin, so the thermal
model matters. Both supported models take wind speed, but they were
calibrated with wind measured at different heights, while forecast
providers report wind at 10 m.

- SAPM (King et al., 2004) coefficients were determined with wind at 10 m.
- Faiman (2008) coefficients were fitted to wind near module height. pvlib
  documents only that wind must be measured "at the same height for which
  the wind loss factor was determined"; the exact anemometer height in the
  paper could not be confirmed from public sources.

## Decision

- **The default thermal model is SAPM, open rack, glass/polymer**, because
  its calibration height matches forecast data and it is the most common
  construction for commercial and industrial rooftop systems.
- **Wind is passed to the thermal model as reported (10 m) by default.**
- **`thermal.WindAtHeight`** (power law, α = 1/7 for open terrain) is
  available for users who choose Faiman and want to scale 10 m wind down to
  module height. It is an explicit opt-in, not a hidden correction.
- Thermal models are a small interface (`CellTemp`, `Validate`) so users can
  plug in their own; configuration files refer to predefined models by name
  through `thermal.ByName`.

## Consequences

With Faiman and uncorrected 10 m wind, cooling is overstated and cell
temperature understated by a few kelvin on windy days. This is documented
in the README as a known limitation.
