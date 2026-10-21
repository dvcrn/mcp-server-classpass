package server

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/dvcrn/mcp-server-classpass/classpass"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Search radius used when the caller passes raw coordinates instead of a named place.
const defaultRadiusKm = 1.2

type searchInput struct {
	Query    string   `json:"query" jsonschema:"activity to search for, e.g. massage, yoga, pilates"`
	Location string   `json:"location,omitempty" jsonschema:"place name, e.g. Shibuya or Sangenjaya Station; required unless lat and lon are given"`
	Lat      *float64 `json:"lat,omitempty" jsonschema:"latitude of the search center"`
	Lon      *float64 `json:"lon,omitempty" jsonschema:"longitude of the search center"`
	Date     string   `json:"date,omitempty" jsonschema:"day to search in YYYY-MM-DD; defaults to today"`
}

type slot struct {
	ScheduleID      int64  `json:"schedule_id"`
	Start           string `json:"start"`
	End             string `json:"end"`
	DurationMinutes int    `json:"duration_minutes"`
	Credits         int    `json:"credits"`
	Status          string `json:"status"`
}

type class struct {
	ClassID int64  `json:"class_id"`
	Name    string `json:"name"`
	Slots   []slot `json:"slots"`
}

type venue struct {
	VenueID    int64   `json:"venue_id"`
	Name       string  `json:"name"`
	Address    string  `json:"address"`
	DistanceKm float64 `json:"distance_km"`
	Rating     float64 `json:"rating"`
	Ratings    int     `json:"ratings"`
	Classes    []class `json:"classes"`
}

type searchOutput struct {
	Location string  `json:"location"`
	Activity string  `json:"activity"`
	Date     string  `json:"date"`
	Venues   []venue `json:"venues"`
}

func (h *handlers) search(ctx context.Context, _ *mcp.CallToolRequest, in searchInput) (*mcp.CallToolResult, searchOutput, error) {
	out, err := search(ctx, h.cp, in)

	return nil, out, err
}

func search(ctx context.Context, cp *classpass.Client, in searchInput) (searchOutput, error) {
	date := in.Date
	if date == "" {
		date = time.Now().Format(time.DateOnly)
	} else if _, err := time.Parse(time.DateOnly, date); err != nil {
		return searchOutput{}, fmt.Errorf("date must be YYYY-MM-DD: %w", err)
	}

	f := classpass.SearchFilters{Date: date, RadiusKm: defaultRadiusKm}
	var locName string

	switch {
	case in.Lat != nil && in.Lon != nil:
		f.Lat, f.Lon = *in.Lat, *in.Lon
		locName = fmt.Sprintf("%f,%f", f.Lat, f.Lon)

	case in.Location != "":
		place, err := resolveLocation(ctx, cp, in.Location)
		if err != nil {
			return searchOutput{}, err
		}

		f.Lat, f.Lon = place.Lat, place.Lon
		if place.RadiusKm > 0 {
			f.RadiusKm = place.RadiusKm
		}

		locName = place.FormattedAddress

	default:
		return searchOutput{}, errors.New("either location or lat and lon is required")
	}

	acts, err := cp.Activities(ctx, in.Query, f.Lat, f.Lon)
	if err != nil {
		return searchOutput{}, err
	}

	if len(acts) == 0 {
		return searchOutput{}, fmt.Errorf("no activity matches %q", in.Query)
	}

	f.Activity = acts[0]

	venues, err := cp.Search(ctx, f)
	if err != nil {
		return searchOutput{}, err
	}

	out := searchOutput{Location: locName, Activity: f.Activity.Name, Date: date, Venues: []venue{}}
	for _, v := range venues {
		if len(v.Classes) == 0 {
			continue
		}

		out.Venues = append(out.Venues, toVenue(v))
	}

	return out, nil
}

func resolveLocation(ctx context.Context, cp *classpass.Client, query string) (*classpass.Place, error) {
	preds, err := cp.LocationAutocomplete(ctx, query)
	if err != nil {
		return nil, err
	}

	if len(preds) == 0 {
		return nil, fmt.Errorf("no place matches %q", query)
	}

	uid, err := cp.UserID(ctx)
	if err != nil {
		return nil, err
	}

	return cp.ResolvePlace(ctx, uid, preds[0].PlaceID)
}

func toVenue(v classpass.Venue) venue {
	loc, err := time.LoadLocation(v.TZ)
	if err != nil {
		loc = time.UTC
	}

	name := v.Name
	if v.Subtitle != "" {
		name += " " + v.Subtitle
	}

	ov := venue{
		VenueID:    v.ID,
		Name:       name,
		Address:    joinAddress(v.Address),
		DistanceKm: v.DistanceKm,
		Rating:     v.Ratings.Mean,
		Ratings:    v.Ratings.Count.Total,
	}

	for _, c := range v.Classes {
		oc := class{ClassID: c.ID, Name: c.Name, Slots: []slot{}}
		for _, s := range c.Schedules {
			oc.Slots = append(oc.Slots, slot{
				ScheduleID:      s.ID,
				Start:           time.Unix(s.StartTime, 0).In(loc).Format(time.RFC3339),
				End:             time.Unix(s.EndTime, 0).In(loc).Format(time.RFC3339),
				DurationMinutes: s.DurationMinutes,
				Credits:         s.Availability.Credits,
				Status:          s.Availability.Status,
			})
		}

		ov.Classes = append(ov.Classes, oc)
	}

	return ov
}
