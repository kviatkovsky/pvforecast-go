// Package solarpos computes the apparent position of the Sun for a site and
// instant.
//
// Two algorithms share the [Algorithm] interface:
//
//   - SPA, the NREL Solar Position Algorithm (Reda & Andreas, 2004,
//     NREL/TP-560-34302), with a stated uncertainty of ±0.0003° for the years
//     -2000 to 6000. It is the reference implementation of this module.
//   - NOAA, a faster and less accurate algorithm based on Meeus, intended for
//     bulk work where hundredth-of-a-degree accuracy is enough.
//
// Angles are in degrees at the API boundary. Azimuth is measured clockwise
// from geographic north. Times are interpreted as UTC instants.
package solarpos
