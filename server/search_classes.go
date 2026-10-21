package server

import (
	"cmp"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/dvcrn/mcp-server-classpass/classpass"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	defaultLimit         = 10
	maxLimit             = 50
	defaultSlotsPerClass = 5
)

type searchInput struct {
	Query string `json:"query,omitempty" jsonschema:"activity to search for, e.g. massage, yoga, pilates; omit to browse everything nearby"`
	TagID string `json:"tag_id,omitempty" jsonschema:"exact activity tag id from list_tags, instead of query"`
	locationArgs
	Date          string `json:"date,omitempty" jsonschema:"day to search in YYYY-MM-DD; defaults to today"`
	EndDate       string `json:"end_date,omitempty" jsonschema:"last day to search in YYYY-MM-DD, at most 3 days after date; one request per day"`
	MaxCredits    *int   `json:"max_credits,omitempty" jsonschema:"only slots costing at most this many credits"`
	TimeAfter     string `json:"time_after,omitempty" jsonschema:"earliest slot start, venue-local HH:MM, e.g. 07:30"`
	TimeBefore    string `json:"time_before,omitempty" jsonschema:"latest slot start, venue-local HH:MM, e.g. 18:00"`
	Limit         int    `json:"limit,omitempty" jsonschema:"max venues to return, default 10, max 50"`
	SlotsPerClass int    `json:"slots_per_class,omitempty" jsonschema:"max slots listed per class, default 5"`
	Cursor        string `json:"cursor,omitempty" jsonschema:"cursor from a previous result to get more venues; repeat the other arguments unchanged"`
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

	// MatchedVenues are venues named like the query when it is not an activity and matches several venues.
	MatchedVenues []foundVenue `json:"matched_venues,omitempty"`
	Cursor        string       `json:"cursor,omitempty"`
	Note          string       `json:"note,omitempty"`
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
	date, err := parseDate(ctx, cp, in.Date)
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

	if in.Query != "" && in.TagID != "" {
		return searchOutput{}, errors.New("pass either query or tag_id, not both")
	}

	c, err := resolveCenter(ctx, cp, in.locationArgs)
	if err != nil {
		return searchOutput{}, err
	}

	f := classpass.SearchFilters{Date: date, Lat: c.Lat, Lon: c.Lon, RadiusKm: c.RadiusKm}
	out := searchOutput{
		Date:           date,
		Venues:         []venue{},
		Location:       c.Name,
		PlaceID:        c.PlaceID,
		OtherLocations: c.Others,
		RadiusKm:       c.RadiusKm,
	}

	days, err := dateRange(date, in.EndDate)
	if err != nil {
		return searchOutput{}, err
	}

	multiDay := len(days) > 1
	if multiDay {
		out.EndDate = days[len(days)-1]
	}

	switch {
	case in.TagID != "":
		tag, err := findTag(ctx, cp, in.TagID)
		if err != nil {
			return searchOutput{}, err
		}

		f.Activity = &classpass.Activity{ID: tag.ID, Name: tag.Name}

	case in.Query != "":
		m, err := resolveActivity(ctx, cp, in.Query, f.Lat, f.Lon)
		if err != nil {
			return searchOutput{}, err
		}

		if m.Activity == nil {
			return searchVenueByName(ctx, cp, m.Venues, days, sf, out)
		}

		f.Activity = m.Activity
	}

	if f.Activity != nil {
		out.Activity = f.Activity.Name
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
		out.Note = emptyNote(ctx, cp, f, out.Cursor != "")
	}

	return out, nil
}

// emptyNote explains an empty result. With an activity it counts matching venues in range, so "closed or fully
// booked" can be told apart from "none nearby".
func emptyNote(ctx context.Context, cp *classpass.Client, f classpass.SearchFilters, morePages bool) string {
	if morePages {
		return "Nothing matched on this page of results. Pass cursor to check the next page."
	}

	generic := "Nothing bookable matched. Try another date, a larger radius_km, a broader or no query, or looser filters."
	if f.Activity == nil {
		return generic
	}

	page, err := cp.SearchVenues(ctx, classpass.VenueSearchFilters{
		Lat: f.Lat, Lon: f.Lon, RadiusKm: f.RadiusKm, Date: f.Date, TagID: f.Activity.ID, Count: 1,
	})
	if err != nil {
		return generic
	}

	if page.TotalHits == 0 {
		return fmt.Sprintf("No %s venues within radius_km. Try a larger radius_km or another location.", f.Activity.Name)
	}

	return fmt.Sprintf("%d %s venues are within radius_km, but none has a bookable slot for these dates and filters; "+
		"they may be closed or fully booked. search_venues lists them; try another date or time.", page.TotalHits, f.Activity.Name)
}

// searchVenueByName answers a query that names venues rather than an activity. A single match gets its slots
// directly, the way list_slots would; several matches are returned for the caller to pick from.
func searchVenueByName(ctx context.Context, cp *classpass.Client, matches []classpass.VenueMatch, days []string, sf slotFilter, out searchOutput) (searchOutput, error) {
	for _, m := range matches {
		out.MatchedVenues = append(out.MatchedVenues, foundVenue{
			VenueID:    m.ID,
			Name:       strings.TrimSpace(m.Name + " " + m.Subtitle),
			DistanceKm: m.DistanceKm,
		})
	}

	if len(matches) > 1 {
		out.Note = "The query names venues rather than an activity. Pick one from matched_venues and call list_slots with its venue_id."
		return out, nil
	}

	m := out.MatchedVenues[0]

	offerings, err := cp.VenueOfferings(ctx, m.VenueID)
	if errors.Is(err, classpass.ErrNotFound) {
		out.Note = "The query names this venue, which does not publish a class list. Search near it by activity instead."
		return out, nil
	}

	if err != nil {
		return searchOutput{}, err
	}

	if len(offerings) > maxVenueClasses {
		out.Note = fmt.Sprintf("The query names this venue, which has %d classes. Call list_slots with its venue_id to pick one.", len(offerings))
		return out, nil
	}

	v := venue{VenueID: m.VenueID, Name: m.Name, DistanceKm: m.DistanceKm, Classes: []class{}}
	for _, o := range offerings {
		c, err := classSlots(ctx, cp, m.VenueID, o.ID, days[0], days[len(days)-1], sf)
		if err != nil {
			return searchOutput{}, err
		}

		if len(c.Slots) > 0 {
			v.Classes = append(v.Classes, c)
		}
	}

	out.MatchedVenues = nil
	out.Venues = []venue{v}
	if len(v.Classes) == 0 {
		out.Note = "The query names this venue, but it has no bookable slots for these dates and filters."
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

// activityMatch is what a free-text query resolved to: an activity, or else venues whose names match it.
type activityMatch struct {
	Activity *classpass.Activity
	Venues   []classpass.VenueMatch
}

// resolveActivity maps a query to an activity whose name matches it, falling back to venues named like it.
// ClassPass ranks unrelated activities first for venue names (e.g. "Free" for "B+TREE"), so the first activity
// suggestion alone is not trusted.
func resolveActivity(ctx context.Context, cp *classpass.Client, query string, lat, lon float64) (activityMatch, error) {
	acts, venues, err := cp.Keywords(ctx, query, lat, lon)
	if err != nil {
		return activityMatch{}, err
	}

	q := strings.ToLower(strings.TrimSpace(query))
	for i, a := range acts {
		name := strings.ToLower(a.Name)
		if name != "" && (strings.Contains(name, q) || strings.Contains(q, name)) {
			return activityMatch{Activity: &acts[i]}, nil
		}
	}

	if len(venues) > 0 {
		return activityMatch{Venues: venues}, nil
	}

	if related := suggestTags(ctx, cp, query); len(related) > 0 {
		return activityMatch{}, fmt.Errorf("no activity matches %q; related tags: %s. Pass one as tag_id", query, strings.Join(related, ", "))
	}

	return activityMatch{}, fmt.Errorf("no activity matches %q; try a broader term, see list_tags, or omit query to browse everything nearby", query)
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
