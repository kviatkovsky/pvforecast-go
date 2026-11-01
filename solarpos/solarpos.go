package solarpos

import (
	"errors"
	"fmt"
	"math"
	"time"
)

// SolarConstant is the nominal total solar irradiance at a distance of one
// astronomical unit, in W/m² (IAU 2015 Resolution B3; Kopp & Lean, 2011).
const SolarConstant = 1361.0

// DefaultTempC is the annual mean air temperature, in °C, assumed for
// refraction when a Site has no Atmosphere. It matches the pvlib default.
const DefaultTempC = 12.0

// Validity limits of the SPA (Reda & Andreas, 2004, Table A4.1). The same
// limits are applied to every Algorithm so that inputs are validated
// consistently.
const (
	MinYear = -2000
	MaxYear = 6000
)

// Sentinel errors returned (wrapped) by Algorithm implementations.
var (
	// ErrInvalidSite reports a site with an out-of-range or non-finite
	// coordinate, altitude, pressure or temperature.
	ErrInvalidSite = errors.New("solarpos: invalid site")
	// ErrOutOfRange reports a time or ΔT outside the algorithm's validity
	// range.
	ErrOutOfRange = errors.New("solarpos: input out of range")
)

// Algorithm computes the position of the Sun as seen from a site.
//
// Implementations are safe for concurrent use and do not allocate on the
// success path.
type Algorithm interface {
	Position(t time.Time, site Site) (Position, error)
}

// Site is an observer location on the Earth.
type Site struct {
	// Lat is the geodetic latitude in degrees, positive north, in [-90, 90].
	Lat float64
	// Lon is the longitude in degrees, positive east, in [-180, 180].
	Lon float64
	// AltitudeM is the height above mean sea level in meters.
	AltitudeM float64
	// Atmosphere sets the conditions used for the refraction correction.
	// If nil, the pressure of the ICAO standard atmosphere at AltitudeM and
	// DefaultTempC are used.
	Atmosphere *Atmosphere
}

// Atmosphere holds the annual mean surface conditions used to correct the
// solar elevation for atmospheric refraction.
type Atmosphere struct {
	PressurePa float64 // annual mean surface pressure, Pa
	TempC      float64 // annual mean air temperature, °C
}

// Position is the position of the Sun as seen from a site.
//
// Angles are in degrees. Azimuth is measured clockwise from geographic north
// (north = 0°, east = 90°). The apparent angles include the refraction
// correction; the others are purely geometric (topocentric).
type Position struct {
	Zenith            float64
	ApparentZenith    float64
	Elevation         float64
	ApparentElevation float64
	Azimuth           float64
	// EarthSunDistance is the Earth–Sun distance in astronomical units.
	EarthSunDistance float64
	// DNIExtra is the extraterrestrial direct normal irradiance in W/m²,
	// SolarConstant scaled by the inverse square of EarthSunDistance.
	DNIExtra float64
}

// Validate reports whether the site is within the ranges accepted by every
// Algorithm. The returned error wraps ErrInvalidSite.
func (s Site) Validate() error {
	switch {
	case !inRange(s.Lat, -90, 90):
		return fmt.Errorf("%w: latitude %g outside [-90, 90]", ErrInvalidSite, s.Lat)
	case !inRange(s.Lon, -180, 180):
		return fmt.Errorf("%w: longitude %g outside [-180, 180]", ErrInvalidSite, s.Lon)
	case !inRange(s.AltitudeM, -6.5e6, math.MaxFloat64):
		// SPA accepts any altitude above -6500 km (Table A4.1).
		return fmt.Errorf("%w: altitude %g m is not a finite value above -6500 km", ErrInvalidSite, s.AltitudeM)
	}
	if a := s.Atmosphere; a != nil {
		switch {
		case !inRange(a.PressurePa, 0, 5e5) || a.PressurePa == 0:
			return fmt.Errorf("%w: pressure %g Pa outside (0, 500000]", ErrInvalidSite, a.PressurePa)
		case !inRange(a.TempC, -273, 6000) || a.TempC == -273:
			// The refraction formula (SPA eq. 42) divides by 273 + T.
			return fmt.Errorf("%w: temperature %g °C outside (-273, 6000]", ErrInvalidSite, a.TempC)
		}
	}
	return nil
}

// conditions returns the refraction pressure (hPa) and temperature (°C).
func (s Site) conditions() (pressureHPa, tempC float64) {
	if s.Atmosphere != nil {
		return s.Atmosphere.PressurePa / 100, s.Atmosphere.TempC
	}
	return StandardPressure(s.AltitudeM) / 100, DefaultTempC
}

// StandardPressure returns the pressure, in Pa, of the ICAO standard
// atmosphere at the given altitude in meters (ICAO Doc 7488, troposphere:
// p = p0·(1 − L·h/T0)^(g·M/(R·L))). The altitude is treated as geopotential;
// the difference from geometric altitude is negligible for refraction.
func StandardPressure(altitudeM float64) float64 {
	const (
		p0       = 101325.0 // Pa
		lapse    = 0.0065   // K/m
		t0       = 288.15   // K
		exponent = 5.25588  // g·M/(R·L)
	)
	base := 1 - lapse*altitudeM/t0
	if base <= 0 {
		// The formula reaches zero pressure near 44 km; above that (only
		// reachable with unrealistic inputs) there is no atmosphere to refract.
		return 0
	}
	return p0 * math.Pow(base, exponent)
}

// checkTime validates the time and returns the Julian day (UT).
func checkTime(t time.Time) (float64, error) {
	t = t.UTC()
	if y := t.Year(); y < MinYear || y > MaxYear {
		return 0, fmt.Errorf("%w: year %d outside [%d, %d]", ErrOutOfRange, y, MinYear, MaxYear)
	}
	return julianDay(t), nil
}

// julianDay returns the Julian day of the instant t. Unix seconds are used
// instead of UnixNano because the latter overflows outside 1678–2262.
func julianDay(t time.Time) float64 {
	const unixEpochJD = 2440587.5
	sec := float64(t.Unix()) + float64(t.Nanosecond())/1e9
	return unixEpochJD + sec/86400
}

func inRange(v, lo, hi float64) bool {
	// NaN fails both comparisons, so it is rejected here too.
	return v >= lo && v <= hi
}

const (
	deg2rad = math.Pi / 180
	rad2deg = 180 / math.Pi
)

// limitDegrees maps an angle in degrees to [0, 360).
func limitDegrees(d float64) float64 {
	d = math.Mod(d, 360)
	if d < 0 {
		d += 360
	}
	return d
}
