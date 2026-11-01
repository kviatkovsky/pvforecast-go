// Command pvforecast prints an hourly AC power and daily energy forecast
// for a PV system, using the Open-Meteo weather forecast.
//
// Usage:
//
//	pvforecast --lat 50.06 --lon 19.94 --kwp 10 --tilt 35 --azimuth 180 --days 2
//	pvforecast --config system.json --format json
//	pvforecast version
//
// Run "pvforecast --help" for all flags. Exit codes: 0 on success, 1 on a
// runtime error (for example, the weather service is unreachable), 2 on a
// usage error (invalid flags or configuration).
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/kviatkovsky/pvforecast-go/weather/openmeteo"
)

// version is set at build time with -ldflags "-X main.version=v1.2.3".
var version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr, deps{
		provider: openmeteo.New(),
		now:      time.Now,
	})
	stop()
	os.Exit(code)
}
