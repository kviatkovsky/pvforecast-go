package openmeteo_test

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/kviatkovsky/pvforecast-go/weather"
	"github.com/kviatkovsky/pvforecast-go/weather/openmeteo"
)

// The example needs network access, so it is compiled but not run.
func ExampleClient_Forecast() {
	client := openmeteo.New(
		openmeteo.WithHTTPClient(&http.Client{Timeout: 20 * time.Second}),
		openmeteo.WithRetry(3, time.Second),
	)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	start := time.Now().UTC().Truncate(time.Hour)
	series, err := client.Forecast(ctx, weather.Request{
		Lat: 50.0647, Lon: 19.9450,
		Start: start, End: start.Add(24 * time.Hour),
	})
	if err != nil {
		fmt.Println(err)
		return
	}
	for _, s := range series.Samples[:3] {
		fmt.Printf("%s  GHI %4.0f W/m²  %4.1f °C\n", s.Time.Format("15:04"), s.GHI, s.AirTempC)
	}
}
