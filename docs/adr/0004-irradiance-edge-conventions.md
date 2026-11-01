# 4. Irradiance conventions near the horizon and for noisy inputs

- Status: accepted
- Date: 2026-11-01

## Context

The transposition and decomposition models divide by cos z, use air mass,
or take ratios such as DNI/DHI. All of these become unstable as the sun
approaches the horizon, and weather forecasts contain small negative or
inconsistent values. pvlib handles this with a set of clamps; it is the
de facto reference, and this module is validated against it.

## Decision

Follow pvlib wherever the result is physically sensible:

| Where | Convention | Origin |
|---|---|---|
| Hay-Davies beam ratio | cos z ≥ 0.01745 (cos 89°) | pvlib |
| Perez circumsolar | cos z ≥ cos 85° | pvlib |
| Perez sky diffuse | clipped at zero; all components zero if the total is not positive | pvlib |
| Erbs clearness index | cos z ≥ 0.065, Kt ∈ [0, 1] | pvlib |
| Erbs above 87° | all of GHI is diffuse | pvlib |
| Air mass above 90° | NaN | pvlib |

Deviate where pvlib's behavior is not physical, and test each deviation:

- **Negative GHI, DNI and DHI are treated as zero** before any model runs.
  In pvlib, a negative DNI with the sun behind the surface gives a positive
  beam (two negatives multiplied). FuzzTranspose found this case.
- **Zero DHI gives zero sky diffuse.** pvlib returns NaN for Perez, because
  the sky clearness divides by DHI.
- **At or below the horizon (z ≥ 90°) beam and circumsolar are zero and every
  model falls back to isotropic sky diffuse.** pvlib returns zero sky
  diffuse for Perez there (air mass is NaN) but keeps Hay-Davies; twilight
  diffuse light is small but real, and the anisotropic terms are undefined.

## Consequences

Results are identical to pvlib for all physically valid daytime inputs (the
fixtures exclude only the deviations above, which have their own tests).
Forecast pipelines can pass raw provider data without pre-cleaning.
