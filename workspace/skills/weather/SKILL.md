---
name: weather
description: Get current weather and forecasts with verified location matching (no API key required).
homepage: https://open-meteo.com/en/docs
metadata: {"nanobot":{"emoji":"🌤️","requires":{"bins":["curl"]}}}
---

# Weather

Resolve the location before requesting weather. Prefer Open-Meteo for both geocoding and forecasts; use wttr.in only as a coordinate-based fallback.

## Workflow

1. Identify the requested location and time range. Ask for a location if none was provided.
2. Geocode place names and select the correct result using the checks below.
3. Request weather using the selected latitude and longitude with `timezone=auto`.
4. Verify the response and report the resolved place, local observation time, conditions, and units.

## Resolve the Location

Use `curl -G` with `--data-urlencode` so spaces and non-ASCII names are encoded correctly:

```bash
curl -fsS --connect-timeout 10 --max-time 20 -G \
  "https://geocoding-api.open-meteo.com/v1/search" \
  --data-urlencode "name=成都" \
  --data "count=5" \
  --data "language=zh" \
  --data "format=json"
```

Choose `language` to match the user's language. If the user already supplied latitude and longitude, validate the ranges and skip geocoding.

Select a result as follows:

- Honor any province, state, region, or country supplied by the user.
- Compare `name`, `feature_code`, `admin1`, `admin2`, `country`, and `population`; never trust the first result blindly.
- Prefer an exact name match and an appropriate populated-place feature such as `PPLA` or `PPLC`. Use population only as a tie-breaker.
- Do not distrust native-script results automatically. If results are empty or suspicious, retry once with an English name or pinyin and compare the metadata and coordinates.
- Ask a concise clarification only when distinct populated places remain plausible. Do not ask merely because the API returned several entries.
- Never silently choose a different country or region just to return an answer.

## Fetch the Forecast

Replace the coordinates with the selected values. Default to one day unless the user requested a longer range:

```bash
curl -fsS --connect-timeout 10 --max-time 20 -G \
  "https://api.open-meteo.com/v1/forecast" \
  --data-urlencode "latitude=30.66667" \
  --data-urlencode "longitude=104.06667" \
  --data "current=temperature_2m,relative_humidity_2m,apparent_temperature,is_day,precipitation,rain,weather_code,wind_speed_10m,wind_direction_10m" \
  --data "daily=weather_code,temperature_2m_max,temperature_2m_min,precipitation_probability_max,precipitation_sum,wind_speed_10m_max" \
  --data "forecast_days=1" \
  --data "timezone=auto"
```

Use Celsius and km/h by default. For US units, add:

```bash
  --data "temperature_unit=fahrenheit" \
  --data "wind_speed_unit=mph" \
  --data "precipitation_unit=inch"
```

## Validate and Present

- Check for an API error and confirm that `current` or `daily` contains the requested data.
- Confirm that the response timezone matches the selected location. Use `current.time` as the observation time.
- Translate WMO weather codes into plain language: 0 clear; 1–3 cloud cover; 45/48 fog; 51–57 drizzle or freezing drizzle; 61–67 rain or freezing rain; 71–77 snow; 80–86 showers; 95–99 thunderstorms, possibly with hail.
- State the resolved place as `name, admin1, country`, for example `Chengdu, Sichuan, China`.
- Report current conditions separately from the daily forecast. Include temperature, apparent temperature, humidity, wind, and precipitation when available.
- Include units and the local observation time. Do not dump raw JSON or numeric weather codes.
- If values are missing, omit them rather than guessing.

## Fallback

If Open-Meteo is unavailable after one retry, query wttr.in with the already verified coordinates, never with an ambiguous raw city name:

```bash
curl -fsS --connect-timeout 10 --max-time 20 \
  "https://wttr.in/30.66667,104.06667?format=j1&m"
```

Use `nearest_area` to confirm the fallback location and `current_condition[0].observation_time` for the observation time. State that the data came from the fallback provider. If geocoding itself is unavailable, ask for coordinates or a more specific place instead of querying an ambiguous city name.

Docs: https://open-meteo.com/en/docs
