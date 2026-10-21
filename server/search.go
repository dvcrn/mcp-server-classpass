package server

import (
	"cmp"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/dvcrn/mcp-server-classpass/classpass"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	defaultRadiusKm      = 3.0
	defaultLimit         = 10
	maxLimit             = 50
	defaultSlotsPerClass = 5
)

type searchInput struct {
	Query         string   `json:"query,omitempty" jsonschema:"activity to search for, e.g. massage, yoga, pilates; omit to browse everything nearby"`
	Location      string   `json:"location,omitempty" jsonschema:"place name, e.g. Shibuya or Sangenjaya; required unless place_id or lat and lon are given"`
	PlaceID       string   `json:"place_id,omitempty" jsonschema:"exact place from search_locations or a previous result's other_locations"`
	Lat           *float64 `json:"lat,omitempty" jsonschema:"latitude of the search center"`
	Lon           *float64 `json:"lon,omitempty" jsonschema:"longitude of the search center"`
	RadiusKm      *float64 `json:"radius_km,omitempty" jsonschema:"search radius in km; defaults to 3, or the size of the named place if larger"`
	Date          string   `json:"date,omitempty" jsonschema:"day to search in YYYY-MM-DD; defaults to today"`
	EndDate       string   `json:"end_date,omitempty" jsonschema:"last day to search in YYYY-MM-DD, at most 3 days after date; one request per day"`
	MaxCredits    *int     `json:"max_credits,omitempty" jsonschema:"only slots costing at most this many credits"`
	TimeAfter     string   `json:"time_after,omitempty" jsonschema:"earliest slot start, venue-local HH:MM, e.g. 07:30"`
	TimeBefore    string   `json:"time_before,omitempty" jsonschema:"latest slot start, venue-local HH:MM, e.g. 18:00"`
	Limit         int      `json:"limit,omitempty" jsonschema:"max venues to return, default 10, max 50"`
	SlotsPerClass int      `json:"slots_per_class,omitempty" jsonschema:"max slots listed per class, default 5"`
	Cursor        string   `json:"cursor,omitempty" jsonschema:"cursor from a previous result to get more venues; repeat the other arguments unchanged"`
}

type slot struct {
	SlotID          int64  `json:"slot_id"`
	Start           string `json:"start"`
	End             string `json:"end"`
	DurationMinutes int    `json:"duration_minutes"`
	Credits         int    `json:"credits"`
	Status          string `json:"status"`
}

type class struct {
	ClassID   int64  `json:"class_id"`
	Name      string `json:"name"`
	Slots     []slot `json:"slots"`
	MoreSlots int    `json:"more_slots,omitempty"`
}

type venue struct {
	VenueID    int64   `json:"venue_id"`
	Name       string  `json:"name"`
	Address    string  `json:"address"`
	Activities string  `json:"activities,omitempty"`
	DistanceKm float64 `json:"distance_km"`
	Rating     float64 `json:"rating"`
	Ratings    int     `json:"ratings"`
	Classes    []class `json:"classes"`
}

type searchOutput struct {
	Location       string           `json:"location"`
	PlaceID        string           `json:"place_id,omitempty"`
	OtherLocations []placeCandidate `json:"other_locations,omitempty"`
	RadiusKm       float64          `json:"radius_km"`
	Activity       string           `json:"activity,omitempty"`
	Date           string           `json:"date"`
	EndDate        string           `json:"end_date,omitempty"`
	Venues         []venue          `json:"venues"`
	Cursor         string           `json:"cursor,omitempty"`
	Note           string           `json:"note,omitempty"`
}

// searchCursor resumes a search: API is the upstream page cursor, Skip the venues of that page already returned.
type searchCursor struct {
	API  string `json:"a,omitempty"`
	Skip int    `json:"s"`
}

// slotFilter holds the per-slot filters; zero values mean no filtering.
type slotFilter struct {
	maxCredits    *int
	after, before string
	perClass      int
}

func (h *handlers) searchClasses(ctx context.Context, _ *mcp.CallToolRequest, in searchInput) (*mcp.CallToolResult, searchOutput, error) {
	out, err := search(ctx, h.cp, in)

	return nil, out, err
}

func search(ctx context.Context, cp *classpass.Client, in searchInput) (searchOutput, error) {
	date, err := parseDate(in.Date)
	if err != nil {
		return searchOutput{}, err
	}

	sf, err := newSlotFilter(in.MaxCredits, in.TimeAfter, in.TimeBefore, cmp.Or(in.SlotsPerClass, defaultSlotsPerClass))
	if err != nil {
		return searchOutput{}, err
	}

	cur, err := decodeCursor(in.Cursor)
	if err != nil {
		return searchOutput{}, err
	}

	limit := cmp.Or(in.Limit, defaultLimit)
	if limit < 1 || limit > maxLimit {
		return searchOutput{}, fmt.Errorf("limit must be between 1 and %d", maxLimit)
	}

	if in.RadiusKm != nil && *in.RadiusKm <= 0 {
		return searchOutput{}, errors.New("radius_km must be positive")
	}

	f := classpass.SearchFilters{Date: date, RadiusKm: defaultRadiusKm}
	out := searchOutput{Date: date, Venues: []venue{}}

	switch {
	case in.Lat != nil && in.Lon != nil:
		f.Lat, f.Lon = *in.Lat, *in.Lon
		out.Location = fmt.Sprintf("%f,%f", f.Lat, f.Lon)

	case in.PlaceID != "" || in.Location != "":
		placeID := in.PlaceID
		if placeID == "" {
			candidates, err := placeCandidates(ctx, cp, in.Location, nil)
			if err != nil {
				return searchOutput{}, err
			}

			placeID = candidates[0].PlaceID
			out.OtherLocations = candidates[1:]
		}

		uid, err := cp.UserID(ctx)
		if err != nil {
			return searchOutput{}, err
		}

		place, err := cp.ResolvePlace(ctx, uid, placeID)
		if err != nil {
			return searchOutput{}, err
		}

		f.Lat, f.Lon = place.Lat, place.Lon
		f.RadiusKm = max(f.RadiusKm, place.RadiusKm)
		out.Location = place.FormattedAddress
		out.PlaceID = placeID

	default:
		return searchOutput{}, errors.New("one of location, place_id, or lat and lon is required")
	}

	if in.RadiusKm != nil {
		f.RadiusKm = *in.RadiusKm
	}

	out.RadiusKm = f.RadiusKm

	if in.Query != "" {
		acts, err := cp.Activities(ctx, in.Query, f.Lat, f.Lon)
		if err != nil {
			return searchOutput{}, err
		}

		if len(acts) == 0 {
			return searchOutput{}, fmt.Errorf("no activity matches %q; try a broader term, or omit query to browse everything nearby", in.Query)
		}

		f.Activity = &acts[0]
		out.Activity = acts[0].Name
	}

	days, err := dateRange(date, in.EndDate)
	if err != nil {
		return searchOutput{}, err
	}

	multiDay := len(days) > 1
	if multiDay {
		out.EndDate = days[len(days)-1]
	}

	// Upstream cursors belong to one day's results, so a multi-day cursor only carries the skip count.
	if multiDay && cur.API != "" {
		return searchOutput{}, errors.New("this cursor is for a single-day search; repeat the arguments it came from")
	}

	venues, next, err := fetchVenues(ctx, cp, f, days, cur.API)
	if err != nil {
		return searchOutput{}, err
	}

	var matched []venue
	for _, v := range venues {
		if ov, ok := toVenue(v, sf); ok {
			matched = append(matched, ov)
		}
	}

	// The API pages by its own ranking, so results are only nearest-first within a page.
	slices.SortStableFunc(matched, func(a, b venue) int { return cmp.Compare(a.DistanceKm, b.DistanceKm) })

	if cur.Skip < len(matched) {
		end := min(cur.Skip+limit, len(matched))
		out.Venues = append(out.Venues, matched[cur.Skip:end]...)

		if end < len(matched) {
			out.Cursor = encodeCursor(searchCursor{API: cur.API, Skip: end})
		}
	}

	if out.Cursor == "" && next != "" {
		if multiDay {
			out.Note = "Some days have more venues than shown. Search a single date and pass cursor to see them."
		} else {
			out.Cursor = encodeCursor(searchCursor{API: next})
		}
	}

	if len(out.Venues) == 0 && out.Note == "" {
		out.Note = "Nothing bookable matched. Try another date, a larger radius_km, a broader or no query, or looser filters."
		if out.Cursor != "" {
			out.Note = "Nothing matched on this page of results. Pass cursor to check the next page."
		}
	}

	return out, nil
}

// newSlotFilter validates the time window; perClass of 0 means no cap.
func newSlotFilter(maxCredits *int, after, before string, perClass int) (slotFilter, error) {
	for _, t := range []string{after, before} {
		if t == "" {
			continue
		}

		if _, err := time.Parse("15:04", t); err != nil {
			return slotFilter{}, fmt.Errorf("time_after and time_before must be HH:MM: %w", err)
		}
	}

	return slotFilter{maxCredits: maxCredits, after: after, before: before, perClass: perClass}, nil
}

func (sf slotFilter) keep(s classpass.Schedule, loc *time.Location) bool {
	if sf.maxCredits != nil && s.Availability.Credits > *sf.maxCredits {
		return false
	}

	// HH:MM strings compare correctly as text.
	hhmm := time.Unix(s.StartTime, 0).In(loc).Format("15:04")
	if sf.after != "" && hhmm < sf.after {
		return false
	}

	if sf.before != "" && hhmm > sf.before {
		return false
	}

	return true
}

// fetchVenues runs the search for each day and merges a venue's classes and slots across days. next is the
// last upstream cursor seen, meaning at least one day has more pages.
func fetchVenues(ctx context.Context, cp *classpass.Client, f classpass.SearchFilters, days []string, cursor string) ([]classpass.Venue, string, error) {
	var venues []classpass.Venue
	var next string
	byID := map[int64]int{}

	for _, d := range days {
		f.Date = d

		page, err := cp.Search(ctx, f, cursor)
		if err != nil {
			return nil, "", err
		}

		next = cmp.Or(page.Cursor, next)

		for _, v := range page.Venues {
			i, ok := byID[v.ID]
			if !ok {
				byID[v.ID] = len(venues)
				venues = append(venues, v)

				continue
			}

			venues[i].Classes = mergeClasses(venues[i].Classes, v.Classes)
		}
	}

	return venues, next, nil
}

func mergeClasses(into, from []classpass.Class) []classpass.Class {
	for _, c := range from {
		i := slices.IndexFunc(into, func(x classpass.Class) bool { return x.ID == c.ID })
		if i < 0 {
			into = append(into, c)
			continue
		}

		for _, s := range c.Schedules {
			if !slices.ContainsFunc(into[i].Schedules, func(x classpass.Schedule) bool { return x.ID == s.ID }) {
				into[i].Schedules = append(into[i].Schedules, s)
			}
		}
	}

	return into
}

// toVenue applies the slot filter and reports false when no slot at the venue survives it.
func toVenue(v classpass.Venue, sf slotFilter) (venue, bool) {
	loc := venueLocation(v.TZ)

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

	if v.Activities != nil {
		ov.Activities = *v.Activities
	}

	for _, c := range v.Classes {
		oc := class{ClassID: c.ID, Name: c.Name, Slots: []slot{}}
		for _, s := range c.Schedules {
			if !sf.keep(s, loc) {
				continue
			}

			if sf.perClass > 0 && len(oc.Slots) == sf.perClass {
				oc.MoreSlots++
				continue
			}

			oc.Slots = append(oc.Slots, toSlot(s, v.TZ))
		}

		if len(oc.Slots) > 0 {
			ov.Classes = append(ov.Classes, oc)
		}
	}

	return ov, len(ov.Classes) > 0
}

func encodeCursor(c searchCursor) string {
	b, _ := json.Marshal(c)

	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeCursor(s string) (searchCursor, error) {
	var c searchCursor
	if s == "" {
		return c, nil
	}

	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return c, errors.New("invalid cursor")
	}

	if err := json.Unmarshal(b, &c); err != nil || c.Skip < 0 {
		return c, errors.New("invalid cursor")
	}

	return c, nil
}
