// Package weather defines the provider-agnostic weather forecast model used
// by the forecast pipeline.
//
// A [Provider] returns a Series of samples (irradiance, air temperature and
// wind speed) together with the metadata needed to interpret them correctly:
// the sampling interval and whether each timestamp labels the start or the
// end of an averaging interval, or an instantaneous value.
//
// Concrete providers live in sub-packages, for example
// [github.com/kviatkovsky/pvforecast-go/weather/openmeteo].
package weather
