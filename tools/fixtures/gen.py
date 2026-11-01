#!/usr/bin/env python3
"""Generate pvlib reference fixtures for the Go test suite.

pvlib is used only as a numerical reference: the Go code is an independent
implementation of the published models. Run via `make fixtures`.
"""

from __future__ import annotations

import argparse
import csv
import math
from pathlib import Path

import pandas as pd
import pvlib

# name, latitude, longitude, altitude (m), annual mean temperature (°C)
LOCATIONS = [
    ("krakow", 50.0647, 19.9450, 219.0, 9.0),
    ("madrid", 40.4168, -3.7038, 667.0, 15.0),
    ("tromso", 69.6492, 18.9553, 10.0, 3.0),
    ("singapore", 1.3521, 103.8198, 15.0, 27.5),
    ("sydney", -33.8688, 151.2093, 39.0, 18.0),
    ("mcmurdo", -77.8460, 166.6760, 10.0, -17.0),
    ("antimeridian", -17.7134, 180.0, 0.0, 25.0),
]

DELTA_T = 67.0  # s, pvlib default; also the Go default

# A non-multiple of one hour so that the samples sweep all hours of the day.
STEP = "11h37min"
YEAR = 2026
EXTRA_TIMES = [
    "1950-06-21T12:00:00Z",
    "2000-01-01T12:00:00Z",
    "2024-02-29T10:30:00Z",
    "2100-12-31T23:59:59Z",
]


def standard_pressure(altitude_m: float) -> float:
    """ICAO standard atmosphere pressure in Pa (same formula as the Go code)."""
    return 101325.0 * (1 - 0.0065 * altitude_m / 288.15) ** 5.25588


def times() -> pd.DatetimeIndex:
    regular = pd.date_range(
        f"{YEAR}-01-01T00:00:00Z", f"{YEAR}-12-31T23:59:59Z", freq=STEP
    )
    return regular.append(pd.DatetimeIndex(EXTRA_TIMES))


def solarpos(out: Path) -> int:
    rows = 0
    with out.open("w", newline="") as f:
        w = csv.writer(f, lineterminator="\n")
        w.writerow([
            "time", "lat", "lon", "altitude_m", "pressure_pa", "temp_c",
            "delta_t", "zenith", "apparent_zenith", "elevation",
            "apparent_elevation", "azimuth", "earth_sun_distance",
        ])
        idx = times()
        for _, lat, lon, alt, temp in LOCATIONS:
            pressure = standard_pressure(alt)
            sp = pvlib.solarposition.spa_python(
                idx, lat, lon, altitude=alt, pressure=pressure,
                temperature=temp, delta_t=DELTA_T, how="numpy",
            )
            dist = pvlib.solarposition.nrel_earthsun_distance(
                idx, how="numpy", delta_t=DELTA_T
            )
            for t, row, r in zip(idx, sp.itertuples(), dist):
                vals = [row.zenith, row.apparent_zenith, row.elevation,
                        row.apparent_elevation, row.azimuth, r]
                assert all(math.isfinite(v) for v in vals)
                w.writerow([
                    t.strftime("%Y-%m-%dT%H:%M:%SZ"), lat, lon, alt,
                    f"{pressure:.6f}", temp, DELTA_T,
                    *(f"{v:.10f}" for v in vals),
                ])
                rows += 1
    return rows


def main() -> None:
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("--out", type=Path, default=Path("internal/testdata"))
    args = p.parse_args()
    args.out.mkdir(parents=True, exist_ok=True)
    n = solarpos(args.out / "solarpos_spa.csv")
    print(f"pvlib {pvlib.__version__}: wrote {n} solar position rows")


if __name__ == "__main__":
    main()
