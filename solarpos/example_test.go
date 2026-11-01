package solarpos_test

import (
	"fmt"
	"time"

	"github.com/kviatkovsky/pvforecast-go/solarpos"
)

func ExampleSPA_Position() {
	site := solarpos.Site{Lat: 50.0647, Lon: 19.9450, AltitudeM: 219}
	morning := time.Date(2026, time.June, 21, 10, 0, 0, 0, time.UTC)

	p, err := solarpos.SPA{}.Position(morning, site)
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Printf("apparent elevation %.2f°, azimuth %.2f°, DNI extra %.0f W/m²\n",
		p.ApparentElevation, p.Azimuth, p.DNIExtra)
	// Output: apparent elevation 62.15°, azimuth 159.03°, DNI extra 1318 W/m²
}

func ExampleNOAA_Position() {
	p, err := solarpos.NOAA{}.Position(
		time.Date(2026, time.June, 21, 10, 0, 0, 0, time.UTC),
		solarpos.Site{Lat: 50.0647, Lon: 19.9450, AltitudeM: 219},
	)
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Printf("apparent elevation %.2f°, azimuth %.2f°\n", p.ApparentElevation, p.Azimuth)
	// Output: apparent elevation 62.15°, azimuth 159.02°
}
