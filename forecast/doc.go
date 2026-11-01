// Package forecast turns a PV system definition and a weather forecast into
// an hourly AC power forecast and daily energy totals.
//
// Compute is the pure, deterministic core of the module: it performs no I/O
// and is what tests and benchmarks exercise. Run wraps it with a
// weather.Provider for the common "fetch and compute" case.
package forecast
