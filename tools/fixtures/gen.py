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

import numpy as np
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


def _fmt(v: float, decimals: int = 8) -> str:
    return "NaN" if not math.isfinite(v) else f"{v:.{decimals}f}"


def irradiance_sun_samples() -> pd.DataFrame:
    """Daytime solar positions, with dense sampling near the horizon."""
    frames = []
    hourly = pd.date_range(f"{YEAR}-01-01T00:30:00Z", periods=8760, freq="1h")
    for name, lat, lon, alt, _ in LOCATIONS[:5]:
        sp = pvlib.solarposition.spa_python(
            hourly, lat, lon, altitude=alt, delta_t=DELTA_T, how="numpy"
        )
        z = sp["apparent_zenith"]
        idx = np.arange(len(z))
        keep = (z < 90) & (((idx % 11) == 0) | ((z > 80) & (idx % 3 == 0)))
        sp = sp[keep]
        frames.append(pd.DataFrame({
            "time": sp.index,
            "zenith": sp["apparent_zenith"].to_numpy(),
            "sun_azimuth": sp["azimuth"].to_numpy(),
        }))
    return pd.concat(frames, ignore_index=True)


def transposition(out: Path) -> int:
    sun = irradiance_sun_samples()
    tilts = [0.0, 10.0, 20.0, 35.0, 60.0, 90.0]
    azimuths = [180.0, 90.0, 270.0, 0.0, 135.0]
    cloud = [1.0, 0.7, 0.35, 0.1]
    albedos = [0.25, 0.2, 0.6]
    n = len(sun)
    i = np.arange(n)
    tilt = np.array(tilts)[i % len(tilts)]
    surf_az = np.array(azimuths)[(i // len(tilts)) % len(azimuths)]
    k = np.array(cloud)[(i // 7) % len(cloud)]
    albedo = np.array(albedos)[(i // 3) % len(albedos)]
    zen = sun["zenith"].to_numpy()
    sun_az = sun["sun_azimuth"].to_numpy()
    times = pd.DatetimeIndex(sun["time"])

    dni_extra = pvlib.irradiance.get_extra_radiation(times).to_numpy()
    ghi = pvlib.clearsky.haurwitz(pd.Series(zen))["ghi"].to_numpy() * k
    dec = pvlib.irradiance.erbs(ghi, zen, times)
    dni, dhi = np.asarray(dec["dni"]), np.asarray(dec["dhi"])
    airmass = np.asarray(pvlib.atmosphere.get_relative_airmass(zen))

    ir = pvlib.irradiance
    aoi = ir.aoi(tilt, surf_az, zen, sun_az)
    beam = ir.beam_component(tilt, surf_az, zen, sun_az, dni)
    iso = ir.isotropic(tilt, dhi)
    hd = ir.haydavies(tilt, surf_az, dhi, dni, dni_extra, zen, sun_az,
                      return_components=True)
    pz = ir.perez(tilt, surf_az, dhi, dni, dni_extra, zen, sun_az, airmass,
                  return_components=True)
    ground = ir.get_ground_diffuse(tilt, ghi, albedo=albedo)

    cols = {
        "tilt": tilt, "surface_azimuth": surf_az, "zenith": zen,
        "sun_azimuth": sun_az, "ghi": ghi, "dni": dni, "dhi": dhi,
        "dni_extra": dni_extra, "airmass": airmass, "albedo": albedo,
        "aoi": aoi, "beam": beam, "isotropic": iso,
        "haydavies": hd["poa_sky_diffuse"],
        "haydavies_isotropic": hd["poa_isotropic"],
        "haydavies_circumsolar": hd["poa_circumsolar"],
        "perez": pz["poa_sky_diffuse"],
        "perez_isotropic": pz["poa_isotropic"],
        "perez_circumsolar": pz["poa_circumsolar"],
        "perez_horizon": pz["poa_horizon"],
        "ground": ground,
    }
    cols = {key: np.asarray(v, dtype=float) for key, v in cols.items()}
    # pvlib returns NaN for Perez when DHI is zero; the Go code returns zero
    # by design and tests that case separately.
    keep = cols["dhi"] > 0
    with out.open("w", newline="") as f:
        w = csv.writer(f, lineterminator="\n")
        w.writerow(cols.keys())
        for r in range(n):
            if keep[r]:
                w.writerow(_fmt(cols[key][r]) for key in cols)
    return int(keep.sum())


def airmass(out: Path) -> int:
    zen = np.concatenate([np.arange(0, 90, 0.5), [89.5, 89.9, 90.0, 90.5, 120.0]])
    am = np.asarray(pvlib.atmosphere.get_relative_airmass(zen))
    with out.open("w", newline="") as f:
        w = csv.writer(f, lineterminator="\n")
        w.writerow(["zenith", "airmass"])
        for z, a in zip(zen, am):
            w.writerow([_fmt(z), _fmt(a)])
    return len(zen)


def erbs(out: Path) -> int:
    rows = 0
    doys = [1, 80, 172, 266, 355]
    ghis = [0.0, 5.0, 50.0, 150.0, 300.0, 500.0, 700.0, 900.0, 1100.0, 1300.0]
    zens = [0.0, 15.0, 30.0, 45.0, 60.0, 75.0, 85.0, 86.9, 87.1, 89.5, 95.0]
    with out.open("w", newline="") as f:
        w = csv.writer(f, lineterminator="\n")
        w.writerow(["ghi", "zenith", "dni_extra", "dni", "dhi", "kt"])
        for doy in doys:
            extra = float(pvlib.irradiance.get_extra_radiation(doy))
            for g in ghis:
                for z in zens:
                    r = pvlib.irradiance.erbs(g, z, doy)
                    w.writerow([_fmt(g), _fmt(z), _fmt(extra),
                                _fmt(float(r["dni"])), _fmt(float(r["dhi"])),
                                _fmt(float(r["kt"]))])
                    rows += 1
    return rows


def thermal(out: Path) -> int:
    poas = [0.0, 50.0, 200.0, 500.0, 800.0, 1000.0, 1200.0]
    temps = [-20.0, 0.0, 15.0, 30.0, 45.0]
    winds = [0.0, 0.5, 2.0, 5.0, 10.0, 20.0]
    sapm = pvlib.temperature.TEMPERATURE_MODEL_PARAMETERS["sapm"]
    rows = 0
    with out.open("w", newline="") as f:
        w = csv.writer(f, lineterminator="\n")
        w.writerow(["model", "poa", "temp_air", "wind", "cell_temp"])
        for e in poas:
            for ta in temps:
                for ws in winds:
                    tc = pvlib.temperature.faiman(e, ta, ws, u0=25.0, u1=6.84)
                    w.writerow(["faiman", e, ta, ws, _fmt(float(tc))])
                    rows += 1
                    for name, p in sapm.items():
                        tc = pvlib.temperature.sapm_cell(e, ta, ws, **p)
                        w.writerow([f"sapm_{name}", e, ta, ws, _fmt(float(tc))])
                        rows += 1
    return rows


def main() -> None:
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("--out", type=Path, default=Path("internal/testdata"))
    args = p.parse_args()
    args.out.mkdir(parents=True, exist_ok=True)
    print(f"pvlib {pvlib.__version__}")
    print(f"  solarpos_spa.csv:           {solarpos(args.out / 'solarpos_spa.csv')} rows")
    print(f"  irradiance_transposition.csv: {transposition(args.out / 'irradiance_transposition.csv')} rows")
    print(f"  irradiance_airmass.csv:     {airmass(args.out / 'irradiance_airmass.csv')} rows")
    print(f"  irradiance_erbs.csv:        {erbs(args.out / 'irradiance_erbs.csv')} rows")
    print(f"  thermal.csv:                {thermal(args.out / 'thermal.csv')} rows")


if __name__ == "__main__":
    main()
