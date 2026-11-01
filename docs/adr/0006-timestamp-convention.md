# 6. Timestamp and interval convention

- Status: accepted
- Date: 2026-11-01

## Context

Open-Meteo reports hourly radiation as the mean over the preceding hour,
labeled at the end of the interval: the value at 12:00 is the average from
11:00 to 12:00. Evaluating the sun's position at the label time instead of
the middle of the interval shifts the geometry by 30 minutes. Over a
symmetric clear day the daily totals nearly cancel out, but individual
morning and evening hours change by 10 % or more, and the error is large
around sunrise and sunset. Other providers label intervals at the start, or
report instantaneous values.

## Decision

- Every `weather.Series` carries its `Interval` and a `Label`
  (`IntervalEnd`, `IntervalStart` or `Instant`). Providers set them; the
  pipeline never guesses.
- `forecast.Compute` evaluates solar geometry at the **representative
  instant** of each sample: the interval midpoint for averaged values
  (label − interval/2 for `IntervalEnd`, label + interval/2 for
  `IntervalStart`), the label itself for `Instant`.
- Energy is power × interval and is attributed to the **local date of the
  representative instant**, so the 23:00–24:00 hour counts towards the day
  it belongs to.
- `forecast.Run` selects samples whose representative instant lies in the
  requested window, and asks the provider for one extra hour so that the
  last interval of the window is included.
- `Point.Time` keeps the provider's label unchanged, and `Result.Label`
  says how to read it.

## Consequences

`TestIntervalConvention` checks that interval-end, interval-start and
instantaneous labelings of the same physical data give identical results,
and that the naive labeling would not. The end-to-end comparison with
pvlib uses the same midpoint convention.
