package server

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/dvcrn/mcp-server-classpass/classpass"
)

// maxDays caps date ranges so one tool call fans out to at most this many days of upstream requests.
const maxDays = 4

// parseDate validates a YYYY-MM-DD date, defaulting to today in the user's ClassPass city so "today" does not
// depend on the timezone of the machine running the server.
func parseDate(ctx context.Context, cp *classpass.Client, date string) (string, error) {
	if date == "" {
		return time.Now().In(homeZone(ctx, cp)).Format(time.DateOnly), nil
	}

	if _, err := time.Parse(time.DateOnly, date); err != nil {
		return "", fmt.Errorf("date must be YYYY-MM-DD: %w", err)
	}

	return date, nil
}

// homeZone returns the timezone of the user's ClassPass city, falling back to the server's local zone.
func homeZone(ctx context.Context, cp *classpass.Client) *time.Location {
	home, err := cp.HomeLocation(ctx)
	if err != nil {
		return time.Local
	}

	loc, err := time.LoadLocation(home.Timezone)
	if err != nil {
		return time.Local
	}

	return loc
}

// dateRange returns every day from start to end inclusive; an empty end means just start.
func dateRange(start, end string) ([]string, error) {
	if end == "" {
		return []string{start}, nil
	}

	s, err := time.Parse(time.DateOnly, start)
	if err != nil {
		return nil, fmt.Errorf("date must be YYYY-MM-DD: %w", err)
	}

	e, err := time.Parse(time.DateOnly, end)
	if err != nil {
		return nil, fmt.Errorf("end_date must be YYYY-MM-DD: %w", err)
	}

	if e.Before(s) {
		return nil, errors.New("end_date must not be before date")
	}

	n := int(e.Sub(s).Hours()/24) + 1
	if n > maxDays {
		return nil, fmt.Errorf("end_date can be at most %d days after date", maxDays-1)
	}

	days := make([]string, n)
	for i := range days {
		days[i] = s.AddDate(0, 0, i).Format(time.DateOnly)
	}

	return days, nil
}
