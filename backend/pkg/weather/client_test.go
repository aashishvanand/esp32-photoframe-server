package weather

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// forecastJSON builds an Open-Meteo response shaped like a timezone=auto one:
// a full day of hourly readings on the hour, and a current_weather stamp that
// carries the location's real UTC offset (so it may not be on the hour).
func forecastJSON(tz, currentTime string) string {
	hours := ""
	humidity := ""
	codes := ""
	for h := 0; h < 24; h++ {
		if h > 0 {
			hours += ","
			humidity += ","
			codes += ","
		}
		hours += fmt.Sprintf(`"2026-09-22T%02d:00"`, h)
		// Humidity doubles as the hour marker: reading h == h+1, so a wrong
		// index is obvious in the assertion.
		humidity += fmt.Sprintf("%d", h+1)
		codes += "0"
	}
	return fmt.Sprintf(`{
		"timezone": %q,
		"current_weather": {"temperature": 21.5, "weathercode": 3, "time": %q},
		"hourly": {"time": [%s], "relativehumidity_2m": [%s], "weathercode": [%s]}
	}`, tz, currentTime, hours, humidity, codes)
}

func newTestClient(t *testing.T, body string) *Client {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "auto", r.URL.Query().Get("timezone"),
			"timezone=auto drives the frame's clock; without it the API answers GMT")
		fmt.Fprint(w, body)
	}))
	t.Cleanup(ts.Close)
	return &Client{httpClient: ts.Client(), endpoint: ts.URL}
}

// Whole-hour offset: current_weather.time lines up with hourly.time exactly.
func TestGetWeatherMatchesCurrentHour(t *testing.T) {
	c := newTestClient(t, forecastJSON("Asia/Taipei", "2026-09-22T21:00"))

	got, err := c.GetWeather("25.03", "121.56")
	require.NoError(t, err)

	assert.Equal(t, 22, got.Humidity, "hour 21 carries humidity 22")
	assert.Equal(t, "Asia/Taipei", got.Timezone)
}

// The regression timezone=auto introduced: in zones whose offset isn't a whole
// number of hours, current_weather.time is at :30/:45 while hourly.time stays
// on the hour. An exact string match missed and silently served midnight's
// humidity to everyone in India, Nepal and the Chathams.
func TestGetWeatherMatchesCurrentHourOnFractionalOffsets(t *testing.T) {
	for _, tc := range []struct {
		name, tz, current string
		wantHumidity      int
	}{
		{"Asia/Kolkata +5:30", "Asia/Kolkata", "2026-09-22T18:30", 19},
		{"Asia/Kathmandu +5:45", "Asia/Kathmandu", "2026-09-22T18:45", 19},
		{"Pacific/Chatham +12:45", "Pacific/Chatham", "2026-09-22T01:45", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := newTestClient(t, forecastJSON(tc.tz, tc.current))

			got, err := c.GetWeather("22.57", "88.36")
			require.NoError(t, err)

			assert.Equal(t, tc.wantHumidity, got.Humidity,
				"must read the hour containing %s, not midnight", tc.current)
			assert.Equal(t, tc.tz, got.Timezone)
		})
	}
}

// A current_weather stamp outside the returned day still falls back rather
// than erroring, and the hourly weathercode is left alone.
func TestGetWeatherFallsBackWhenNoHourMatches(t *testing.T) {
	c := newTestClient(t, forecastJSON("Asia/Taipei", "2026-09-25T09:00"))

	got, err := c.GetWeather("25.03", "121.56")
	require.NoError(t, err)

	assert.Equal(t, 1, got.Humidity, "fallback is the first hourly reading")
	assert.Equal(t, 3, got.WeatherCode, "current_weather's code survives a miss")
}

func TestGetWeatherHandlesEmptyHourly(t *testing.T) {
	c := newTestClient(t, `{"timezone":"Asia/Taipei",
		"current_weather":{"temperature":21.5,"weathercode":3,"time":"2026-09-22T21:00"},
		"hourly":{"time":[],"relativehumidity_2m":[],"weathercode":[]}}`)

	got, err := c.GetWeather("25.03", "121.56")
	require.NoError(t, err)

	assert.Equal(t, 0, got.Humidity)
	assert.Equal(t, 3, got.WeatherCode)
}

func TestGetWeatherReportsHTTPError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer ts.Close()
	c := &Client{httpClient: ts.Client(), endpoint: ts.URL}

	_, err := c.GetWeather("25.03", "121.56")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "429")
}

func TestHourKey(t *testing.T) {
	assert.Equal(t, "2026-09-22T18", hourKey("2026-09-22T18:30"))
	assert.Equal(t, "2026-09-22T18", hourKey("2026-09-22T18:00"))
	assert.Equal(t, "", hourKey("2026-09-22"), "too short to name an hour")
	assert.Equal(t, "", hourKey(""))
}
