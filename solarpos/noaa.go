package solarpos

import (
	"math"
	"time"
)

// NOAA is the algorithm of the NOAA Solar Calculator, based on the
// low-precision solar coordinates of Meeus (1998), "Astronomical Algorithms",
// 2nd ed., chapters 25 and 28.
//
// It evaluates a handful of trigonometric terms instead of the SPA's
// periodic series, which makes it several times faster, at the cost of
// accuracy: it ignores ΔT and parallax, and uses a fixed standard-atmosphere
// refraction model that ignores Site.Atmosphere. See the README for the
// measured error against SPA.
//
// The zero value is ready to use.
type NOAA struct{}

var _ Algorithm = NOAA{}

// Position implements Algorithm.
func (NOAA) Position(t time.Time, site Site) (Position, error) {
	if err := site.Validate(); err != nil {
		return Position{}, err
	}
	jd, err := checkTime(t)
	if err != nil {
		return Position{}, err
	}
	return noaa(jd, site.Lat, site.Lon), nil
}

func noaa(jd, lat, lon float64) Position {
	jc := (jd - 2451545) / 36525

	// Geometric mean longitude and anomaly of the Sun, eccentricity of the
	// Earth's orbit (Meeus ch. 25).
	l0 := limitDegrees(280.46646 + jc*(36000.76983+jc*0.0003032))
	m := 357.52911 + jc*(35999.05029-0.0001537*jc)
	ecc := 0.016708634 - jc*(0.000042037+0.0000001267*jc)
	mR := m * deg2rad

	// Equation of the center, true longitude and anomaly, radius vector
	// (Meeus ch. 25).
	c := math.Sin(mR)*(1.914602-jc*(0.004817+0.000014*jc)) +
		math.Sin(2*mR)*(0.019993-0.000101*jc) +
		math.Sin(3*mR)*0.000289
	trueLong := l0 + c
	trueAnomR := (m + c) * deg2rad
	r := 1.000001018 * (1 - ecc*ecc) / (1 + ecc*math.Cos(trueAnomR))

	// Apparent longitude (Meeus ch. 25) and the obliquity of the ecliptic
	// corrected for nutation (Meeus ch. 22).
	omegaR := (125.04 - 1934.136*jc) * deg2rad
	appLongR := (trueLong - 0.00569 - 0.00478*math.Sin(omegaR)) * deg2rad
	meanObliq := 23 + (26+(21.448-jc*(46.815+jc*(0.00059-jc*0.001813)))/60)/60
	obliqR := (meanObliq + 0.00256*math.Cos(omegaR)) * deg2rad

	declR := math.Asin(math.Sin(obliqR) * math.Sin(appLongR))

	// Equation of time in minutes (Meeus eq. 28.3).
	l0R := l0 * deg2rad
	yy := math.Tan(obliqR / 2)
	yy *= yy
	eqTime := 4 * rad2deg * (yy*math.Sin(2*l0R) -
		2*ecc*math.Sin(mR) +
		4*ecc*yy*math.Sin(mR)*math.Cos(2*l0R) -
		0.5*yy*yy*math.Sin(4*l0R) -
		1.25*ecc*ecc*math.Sin(2*mR))

	// True solar time (minutes) and hour angle, from the UT time of day.
	minutesUT := (jd + 0.5 - math.Floor(jd+0.5)) * 1440
	tst := math.Mod(minutesUT+eqTime+4*lon, 1440)
	if tst < 0 {
		tst += 1440
	}
	hR := (tst/4 - 180) * deg2rad

	latR := lat * deg2rad
	cosZen := math.Sin(latR)*math.Sin(declR) + math.Cos(latR)*math.Cos(declR)*math.Cos(hR)
	zenith := rad2deg * math.Acos(math.Max(-1, math.Min(1, cosZen)))
	elev := 90 - zenith

	// The calculator derives azimuth with acos, which is ill-conditioned near
	// the zenith and the poles; this atan2 form is mathematically equivalent.
	azimuth := limitDegrees(180 + rad2deg*math.Atan2(math.Sin(hR),
		math.Cos(hR)*math.Sin(latR)-math.Tan(declR)*math.Cos(latR)))

	appElev := elev + noaaRefraction(elev)
	return Position{
		Zenith:            zenith,
		ApparentZenith:    90 - appElev,
		Elevation:         elev,
		ApparentElevation: appElev,
		Azimuth:           azimuth,
		EarthSunDistance:  r,
		DNIExtra:          SolarConstant / (r * r),
	}
}

// noaaRefraction is the piecewise refraction approximation of the NOAA
// Solar Calculator, in degrees, for a geometric elevation in degrees.
func noaaRefraction(elev float64) float64 {
	var arcsec float64
	switch {
	case elev > 85:
		return 0
	case elev > 5:
		t := math.Tan(elev * deg2rad)
		arcsec = 58.1/t - 0.07/(t*t*t) + 0.000086/(t*t*t*t*t)
	case elev > -0.575:
		arcsec = 1735 + elev*(-518.2+elev*(103.4+elev*(-12.79+elev*0.711)))
	default:
		arcsec = -20.772 / math.Tan(elev*deg2rad)
	}
	return arcsec / 3600
}
