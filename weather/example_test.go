package weather_test

import (
	"fmt"
	"time"

	"github.com/kviatkovsky/pvforecast-go/weather"
)

// Open-Meteo labels an hourly mean at the end of its hour, so the sun for
// the 12:00 value is evaluated at 11:30.
func ExampleLabel_RepresentativeOffset() {
	label := time.Date(2026, time.June, 21, 12, 0, 0, 0, time.UTC)
	for _, l := range []weather.Label{weather.IntervalEnd, weather.IntervalStart, weather.Instant} {
		at := label.Add(l.RepresentativeOffset(time.Hour))
		fmt.Printf("%-14s -> %s\n", l, at.Format("15:04"))
	}
	// Output:
	// interval-end   -> 11:30
	// interval-start -> 12:30
	// instant        -> 12:00
}
