package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strings"
	"time"

	"eumetria/internal/google"
)

// Google Health (Fitbit / Pixel Watch) configuration:
//
//	GOOGLE_CLIENT_ID,          OAuth client from the Google Cloud console.
//	GOOGLE_CLIENT_SECRET,      The redirect URL must be one of the client's
//	GOOGLE_REDIRECT_URL        authorized redirect URIs, character for character.
//
// The refresh token from -google-auth lives in -google-token (private, 0600).

// googleConfig takes the OAuth client from the environment.
func googleConfig() (google.Config, error) {
	cfg := google.Config{
		ClientID:     os.Getenv("GOOGLE_CLIENT_ID"),
		ClientSecret: os.Getenv("GOOGLE_CLIENT_SECRET"),
		RedirectURL:  os.Getenv("GOOGLE_REDIRECT_URL"),
	}
	if cfg.ClientID == "" || cfg.ClientSecret == "" || cfg.RedirectURL == "" {
		return cfg, fmt.Errorf("GOOGLE_CLIENT_ID, GOOGLE_CLIENT_SECRET and GOOGLE_REDIRECT_URL must be set")
	}
	return cfg, nil
}

// googleAuth runs the one-off consent: the user opens the printed URL, grants
// access and pastes back the URL Google redirected to (the page itself does
// not need to handle it; the code is in the address bar).
func googleAuth(ctx context.Context, cfg google.Config, tokenPath string) error {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return err
	}
	state := hex.EncodeToString(b)

	fmt.Printf("1. Open this URL in a browser signed in to the Google account of your watch:\n\n%s\n\n", cfg.AuthURL(state))
	fmt.Printf("2. Allow access. Google then redirects to %s?…code=…\n", cfg.RedirectURL)
	fmt.Printf("3. Paste that whole URL from the address bar here and press Enter:\n> ")

	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && line == "" {
		return fmt.Errorf("reading the redirect URL: %w", err)
	}
	code, err := codeFromRedirect(strings.TrimSpace(line), state)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	hc := &http.Client{Timeout: 30 * time.Second}
	tok, err := cfg.Exchange(ctx, hc, code)
	if err != nil {
		return err
	}
	if err := google.SaveToken(tokenPath, tok); err != nil {
		return fmt.Errorf("saving token: %w", err)
	}
	fmt.Printf("\nSaved the refresh token to %s.\n", tokenPath)
	fmt.Printf("Granted scopes: %s\n", tok.Scope)
	if missing := missingScopes(tok.Scope); len(missing) > 0 {
		fmt.Printf("Not granted (some data will be missing): %s\n", strings.Join(missing, " "))
	}

	c, err := google.New(cfg, tokenPath)
	if err != nil {
		return err
	}
	id, err := c.Raw(ctx, http.MethodGet, "/users/me/identity", nil, nil)
	if err != nil {
		return fmt.Errorf("checking access (is the Google account linked to Fitbit?): %w", err)
	}
	fmt.Printf("Identity: %s\n", strings.TrimSpace(string(id)))
	return nil
}

// codeFromRedirect accepts the redirect URL (checking state) or a bare code.
func codeFromRedirect(s, state string) (string, error) {
	if s == "" {
		return "", errors.New("no redirect URL given")
	}
	if !strings.Contains(s, "code=") && !strings.Contains(s, "error=") {
		return s, nil // a bare code
	}
	u, err := url.Parse(s)
	if err != nil {
		return "", fmt.Errorf("parsing the redirect URL: %w", err)
	}
	q := u.Query()
	if e := q.Get("error"); e != "" {
		return "", fmt.Errorf("google denied access: %s", e)
	}
	if q.Get("state") != state {
		return "", errors.New("the redirect URL belongs to a different authorization attempt (state mismatch); start again")
	}
	if q.Get("code") == "" {
		return "", errors.New("the redirect URL has no code")
	}
	return q.Get("code"), nil
}

func missingScopes(granted string) []string {
	have := map[string]bool{}
	for s := range strings.FieldsSeq(granted) {
		have[s] = true
	}
	var missing []string
	for _, s := range google.Scopes {
		if !have[s] {
			missing = append(missing, strings.TrimPrefix(s, "https://www.googleapis.com/auth/"))
		}
	}
	return missing
}

// debugGoogle prints the raw Health API responses for one day, to pin down
// the real field names before the data goes into the history. Every section
// is tried even when an earlier one fails. It never prints tokens.
func debugGoogle(ctx context.Context, c *google.Client, date string) error {
	day, err := time.ParseInLocation(time.DateOnly, date, time.Local)
	if err != nil {
		return fmt.Errorf("invalid -debug-google %q: %w", date, err)
	}
	next := day.AddDate(0, 0, 1)
	utc := func(t time.Time) string { return t.UTC().Format(time.RFC3339) }

	if id, err := c.Raw(ctx, http.MethodGet, "/users/me/identity", nil, nil); err != nil {
		fmt.Printf("== identity (error: %v)\n", err)
	} else {
		fmt.Printf("== identity\n%s\n", indent(id))
	}

	exercises, err := c.List(ctx, "exercise", "",
		fmt.Sprintf(`exercise.interval.civil_start_time >= "%s" AND exercise.interval.civil_start_time < "%s"`,
			day.Format(time.DateOnly), next.Format(time.DateOnly)), 25)
	printPoints("exercise "+date, exercises, err)
	for _, raw := range exercises {
		var ex struct {
			Name     string `json:"name"`
			Exercise struct {
				Interval struct {
					StartTime time.Time `json:"startTime"`
					EndTime   time.Time `json:"endTime"`
				} `json:"interval"`
				ExerciseMetadata struct {
					HasGPS bool `json:"hasGps"`
				} `json:"exerciseMetadata"`
			} `json:"exercise"`
		}
		_ = json.Unmarshal(raw, &ex)
		if ex.Name == "" {
			continue
		}
		debugWorkoutHeartRate(ctx, c, ex.Exercise.Interval.StartTime, ex.Exercise.Interval.EndTime)
		tcx, err := c.ExerciseTCX(ctx, ex.Name)
		if err != nil {
			fmt.Printf("== tcx %s (hasGps=%v, error: %v)\n", ex.Name, ex.Exercise.ExerciseMetadata.HasGPS, err)
			continue
		}
		s := string(tcx)
		fmt.Printf("== tcx %s (hasGps=%v): %d bytes, %d trackpoints, %d positions, %d heart rate values\n",
			ex.Name, ex.Exercise.ExerciseMetadata.HasGPS, len(tcx),
			strings.Count(s, "<Trackpoint>"), strings.Count(s, "<Position>"), strings.Count(s, "<HeartRateBpm>"))
		if i := strings.Index(s, "<Trackpoint>"); i >= 0 {
			end := strings.Index(s[i:], "</Trackpoint>")
			if end > 0 {
				// Coordinates are masked so the output can be shared.
				tp := coordRe.ReplaceAllString(s[i:i+end], "<$1>X</$1>")
				fmt.Printf("first trackpoint (coordinates masked):\n%s</Trackpoint>\n", tp)
			}
		}
	}

	// One line per workout of the last 30 days, to find a GPS one and to
	// check that the range filter pages through more than 25 results.
	month, err := c.List(ctx, "exercise", "",
		fmt.Sprintf(`exercise.interval.civil_start_time >= "%s" AND exercise.interval.civil_start_time < "%s"`,
			day.AddDate(0, 0, -29).Format(time.DateOnly), next.Format(time.DateOnly)), 25)
	fmt.Printf("== exercise, 30 days up to %s: %d data points (error: %v)\n", date, len(month), err)
	for _, raw := range month {
		var ex struct {
			DataSource struct {
				RecordingMethod string `json:"recordingMethod"`
			} `json:"dataSource"`
			Exercise struct {
				Interval struct {
					StartTime time.Time `json:"startTime"`
				} `json:"interval"`
				ExerciseType     string `json:"exerciseType"`
				DisplayName      string `json:"displayName"`
				ActiveDuration   string `json:"activeDuration"`
				ExerciseMetadata struct {
					HasGPS bool `json:"hasGps"`
				} `json:"exerciseMetadata"`
				MetricsSummary map[string]any `json:"metricsSummary"`
			} `json:"exercise"`
		}
		_ = json.Unmarshal(raw, &ex)
		e := ex.Exercise
		keys := make([]string, 0, len(e.MetricsSummary))
		for k := range e.MetricsSummary {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		fmt.Printf("   %s  %-20s %-22q %-18s gps=%-5v active=%-6s metrics=%s\n",
			e.Interval.StartTime.Local().Format("2006-01-02 15:04"), e.ExerciseType, e.DisplayName,
			ex.DataSource.RecordingMethod, e.ExerciseMetadata.HasGPS, e.ActiveDuration, strings.Join(keys, ","))
	}

	sleep, err := c.List(ctx, "sleep", ":reconcile",
		fmt.Sprintf(`sleep.interval.end_time >= "%s" AND sleep.interval.end_time < "%s"`, utc(day), utc(next)), 25)
	printPoints("sleep ending "+date, sleep, err)
	printSleepCandidates(ctx, c, day, next)

	// Daily totals (rolled up) and the paired devices, as the sync reads them.
	totals, err := c.DailyTotals(ctx, day.AddDate(0, 0, -6), next)
	fmt.Printf("== daily totals, week up to %s (error: %v)\n", date, err)
	for t := day.AddDate(0, 0, -6); t.Before(next); t = t.AddDate(0, 0, 1) {
		if v := totals[t.Format(time.DateOnly)]; v != nil {
			azm := "-"
			if v.ZoneMinutes != nil {
				azm = fmt.Sprintf("fat burn %.0f, cardio %.0f, peak %.0f", v.ZoneMinutes[0], v.ZoneMinutes[1], v.ZoneMinutes[2])
			}
			fmt.Printf("   %s  total %5.0f kcal  zone minutes: %s\n", t.Format(time.DateOnly), v.Kcal, azm)
		}
	}
	if devs, err := c.Raw(ctx, http.MethodGet, "/users/me/pairedDevices", nil, nil); err != nil {
		fmt.Printf("== paired devices (error: %s)\n", compactError(err))
	} else {
		// The MAC address is left out of the output.
		var v map[string][]map[string]any
		_ = json.Unmarshal(devs, &v)
		for _, d := range v["pairedDevices"] {
			delete(d, "macAddress")
		}
		b, _ := json.MarshalIndent(v, "", "  ")
		fmt.Printf("== paired devices\n%s\n", b)
	}

	// Daily summaries carry a civil date. Its filter field is not clearly
	// documented, so try the likely spellings for the week up to the day and
	// report which one the API accepts.
	// "<type>.date" is the right field (the API names other spellings an
	// invalid member), but it rejected ">= AND <=". Try other comparators;
	// without a working one the sync lists newest-first and stops early.
	from, to := day.AddDate(0, 0, -6).Format(time.DateOnly), day.Format(time.DateOnly)
	for _, dt := range []string{
		"daily-resting-heart-rate", "daily-heart-rate-variability", "daily-heart-rate-zones",
		"daily-respiratory-rate", "daily-oxygen-saturation", "daily-sleep-temperature-derivations",
	} {
		snake := strings.ReplaceAll(dt, "-", "_")
		shown := false
		for _, f := range []string{
			fmt.Sprintf(`%s.date >= "%s" AND %s.date < "%s"`, snake, from, snake, next.Format(time.DateOnly)),
			fmt.Sprintf(`%s.date >= "%s"`, snake, from),
			fmt.Sprintf(`%s.date = "%s"`, snake, to),
		} {
			points, err := c.List(ctx, dt, "", f, 0)
			if err != nil {
				fmt.Printf("== %s filter %s\n   error: %s\n", dt, f, compactError(err))
				continue
			}
			fmt.Printf("== %s filter %s\n   OK: %d data points, dates %s\n", dt, f, len(points), pointDates(points))
			if !shown && len(points) > 0 {
				fmt.Printf("   newest:\n%s\n", indent(points[0]))
				shown = true
			}
		}
		if !shown {
			newest, err := c.Raw(ctx, http.MethodGet, "/users/me/dataTypes/"+dt+"/dataPoints", url.Values{"pageSize": {"1"}}, nil)
			fmt.Printf("== %s newest, unfiltered (error: %v)\n%s\n", dt, err, indent(newest))
		}
	}
	return nil
}

// printSleepCandidates prints the raw numbers that might be behind the
// Fitbit app's "sleep quality" bars (time to sound sleep, sound sleep,
// restlessness, interruptions), which the API does not report directly, so
// they can be compared with the app before anything is shown.
func printSleepCandidates(ctx context.Context, c *google.Client, day, next time.Time) {
	nights, err := c.Sleeps(ctx, day, next)
	if err != nil {
		fmt.Printf("== sleep quality candidates (error: %v)\n", err)
		return
	}
	for _, n := range nights {
		var restless, awake5, awakeAll float64
		var restlessN, awake5N int
		for _, r := range n.Restless {
			restless += r.End.Sub(r.Start).Minutes()
			restlessN++
		}
		for _, st := range n.Stages {
			if st.Stage != "awake" {
				continue
			}
			m := st.End.Sub(st.Start).Minutes()
			awakeAll += m
			if m >= 5 {
				awake5 += m
				awake5N++
			}
		}
		fmt.Printf("== sleep quality candidates, %s–%s (main sleep: %v)\n", n.Start.Local().Format("15:04"), n.End.Local().Format("15:04"), n.Main)
		fmt.Printf("   minutesToFallAsleep   %3.0f   minutesAfterWakeUp %3.0f\n", n.ToFallAsleep, n.AfterWakeUp)
		fmt.Printf("   asleep                %3.0f   (%s)   deep+REM %.0f\n", n.Asleep, hm(n.Asleep), n.Deep+n.REM)
		fmt.Printf("   asleep - restless     %3.0f   (%s)\n", n.Asleep-restless, hm(n.Asleep-restless))
		fmt.Printf("   short awakenings      %3.0f min in %d\n", restless, restlessN)
		fmt.Printf("   awake stages >= 5 min %3.0f min in %d   all awake stages %.0f min\n", awake5, awake5N, awakeAll)
	}
}

func hm(min float64) string { return fmt.Sprintf("%dh%02dm", int(min)/60, int(min)%60) }

// debugWorkoutHeartRate shows the heart rate during one workout two ways:
// the raw samples (about one per second) and a per-minute roll-up, to pick
// the cheaper source for the workout's heart rate curve.
func debugWorkoutHeartRate(ctx context.Context, c *google.Client, start, end time.Time) {
	utc := func(t time.Time) string { return t.UTC().Format(time.RFC3339) }
	f := fmt.Sprintf(`heart_rate.sample_time.physical_time >= "%s" AND heart_rate.sample_time.physical_time < "%s"`, utc(start), utc(end))
	samples, err := c.List(ctx, "heart-rate", "", f, 10000)
	fmt.Printf("== heart-rate samples %s – %s: %d (error: %v)\n", start.Local().Format("15:04"), end.Local().Format("15:04"), len(samples), err)
	for i, raw := range samples {
		if i == 3 {
			break
		}
		fmt.Printf("%s\n", indent(raw))
	}

	body := map[string]any{
		"range":      map[string]string{"startTime": utc(start), "endTime": utc(end)},
		"windowSize": "60s",
	}
	roll, err := c.Raw(ctx, http.MethodPost, "/users/me/dataTypes/heart-rate/dataPoints:rollUp", nil, body)
	if err != nil {
		fmt.Printf("== heart-rate rollUp 60s (error: %s)\n", shorten(err.Error(), 300))
		return
	}
	var r struct {
		RollupDataPoints []json.RawMessage `json:"rollupDataPoints"`
		NextPageToken    string            `json:"nextPageToken"`
	}
	_ = json.Unmarshal(roll, &r)
	fmt.Printf("== heart-rate rollUp 60s: %d windows, more pages: %v\n", len(r.RollupDataPoints), r.NextPageToken != "")
	for i, raw := range r.RollupDataPoints {
		if i == 2 {
			break
		}
		fmt.Printf("%s\n", indent(raw))
	}
}

// compactError puts an API error on one line, keeping the reason details
// that the indented body hides past the first few hundred characters.
var coordRe = regexp.MustCompile(`<(LatitudeDegrees|LongitudeDegrees)>[^<]*</(?:LatitudeDegrees|LongitudeDegrees)>`)

func compactError(err error) string {
	var apiErr *google.APIError
	if errors.As(err, &apiErr) {
		var buf bytes.Buffer
		if json.Compact(&buf, []byte(apiErr.Body)) == nil {
			return fmt.Sprintf("HTTP %d %s", apiErr.Status, shorten(buf.String(), 800))
		}
	}
	return shorten(err.Error(), 800)
}

func shorten(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// pointDates lists the civil dates of daily data points, whichever data type
// field holds them.
func pointDates(points []json.RawMessage) string {
	var dates []string
	for _, raw := range points {
		var p map[string]json.RawMessage
		_ = json.Unmarshal(raw, &p)
		for _, v := range p {
			var d struct {
				Date struct{ Year, Month, Day int } `json:"date"`
			}
			if json.Unmarshal(v, &d) == nil && d.Date.Year > 0 {
				dates = append(dates, fmt.Sprintf("%04d-%02d-%02d", d.Date.Year, d.Date.Month, d.Date.Day))
			}
		}
	}
	return "[" + strings.Join(dates, " ") + "]"
}

func printPoints(title string, points []json.RawMessage, err error) {
	if err != nil {
		fmt.Printf("== %s (error: %v)\n", title, err)
	}
	b, _ := json.MarshalIndent(points, "", "  ")
	fmt.Printf("== %s: %d data points\n%s\n", title, len(points), b)
}
