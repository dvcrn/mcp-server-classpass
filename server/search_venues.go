package server

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/dvcrn/mcp-server-classpass/classpass"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	defaultVenueLimit = 20
	maxVenueLimit     = 75
)

type searchVenuesInput struct {
	Name     string `json:"name,omitempty" jsonschema:"venue or studio name, e.g. B+TREE"`
	Activity string `json:"activity,omitempty" jsonschema:"kind of venue, e.g. coffee, juice, massage, sauna"`
	TagID    string `json:"tag_id,omitempty" jsonschema:"exact tag id from list_tags, e.g. 16585 for Food and Drink"`
	locationArgs
	Date  string `json:"date,omitempty" jsonschema:"day to check for bookable venues in YYYY-MM-DD; defaults to today"`
	Limit int    `json:"limit,omitempty" jsonschema:"max venues to return for activity or tag_id, default 20, max 75"`
}

type foundVenue struct {
	VenueID      int64   `json:"venue_id"`
	Name         string  `json:"name"`
	Activities   string  `json:"activities,omitempty"`
	DistanceKm   float64 `json:"distance_km"`
	Rating       float64 `json:"rating,omitempty"`
	RatingsCount string  `json:"ratings_count,omitempty"`
	Address      string  `json:"address,omitempty"`
	OpenNow      *bool   `json:"open_now,omitempty"`
}

type searchVenuesOutput struct {
	Location       string           `json:"location"`
	PlaceID        string           `json:"place_id,omitempty"`
	OtherLocations []placeCandidate `json:"other_locations,omitempty"`
	RadiusKm       float64          `json:"radius_km,omitempty"`
	Tag            string           `json:"tag,omitempty"`
	TotalHits      int              `json:"total_hits,omitempty"`
	Venues         []foundVenue     `json:"venues"`
	Note           string           `json:"note,omitempty"`
}

func (h *handlers) searchVenues(ctx context.Context, _ *mcp.CallToolRequest, in searchVenuesInput) (*mcp.CallToolResult, searchVenuesOutput, error) {
	modes := 0
	for _, s := range []string{in.Name, in.Activity, in.TagID} {
		if s != "" {
			modes++
		}
	}

	if modes != 1 {
		return nil, searchVenuesOutput{}, errors.New("pass exactly one of name, activity, or tag_id")
	}

	limit := cmp.Or(in.Limit, defaultVenueLimit)
	if limit < 1 || limit > maxVenueLimit {
		return nil, searchVenuesOutput{}, fmt.Errorf("limit must be between 1 and %d", maxVenueLimit)
	}

	date, err := parseDate(ctx, h.cp, in.Date)
	if err != nil {
		return nil, searchVenuesOutput{}, err
	}

	c, err := resolveCenter(ctx, h.cp, in.locationArgs)
	if err != nil {
		return nil, searchVenuesOutput{}, err
	}

	out := searchVenuesOutput{Location: c.Name, PlaceID: c.PlaceID, OtherLocations: c.Others, Venues: []foundVenue{}}

	if in.Name != "" {
		out, err := venuesByName(ctx, h.cp, in.Name, c, out)

		return nil, out, err
	}

	tagID, tagName := in.TagID, ""
	if in.Activity != "" {
		act, err := resolveActivity(ctx, h.cp, in.Activity, c.Lat, c.Lon)
		if err != nil {
			return nil, searchVenuesOutput{}, err
		}

		tagID, tagName = act.ID, act.Name
	} else {
		tag, err := findTag(ctx, h.cp, tagID)
		if err != nil {
			return nil, searchVenuesOutput{}, err
		}

		tagName = tag.Name
	}

	page, err := h.cp.SearchVenues(ctx, classpass.VenueSearchFilters{
		Lat: c.Lat, Lon: c.Lon, RadiusKm: c.RadiusKm, Date: date, TagID: tagID, Count: limit,
	})
	if err != nil {
		return nil, searchVenuesOutput{}, err
	}

	out.RadiusKm, out.Tag, out.TotalHits = c.RadiusKm, tagName, page.TotalHits
	for _, v := range page.Venues {
		open := v.IsOpen
		out.Venues = append(out.Venues, foundVenue{
			VenueID:      v.ID,
			Name:         strings.TrimSpace(v.Name + " " + v.Subtitle),
			Activities:   v.Activities,
			DistanceKm:   v.DistanceKm,
			Rating:       v.Rating,
			RatingsCount: v.RatingsCount,
			Address:      joinAddress(v.Address),
			OpenNow:      &open,
		})
	}

	slices.SortStableFunc(out.Venues, func(a, b foundVenue) int { return cmp.Compare(a.DistanceKm, b.DistanceKm) })

	switch {
	case len(out.Venues) == 0:
		out.Note = "No venues with this tag in range. Try a larger radius_km, another location, or a broader tag from list_tags."

	case page.TotalHits > len(out.Venues):
		out.Note = fmt.Sprintf("Showing %d of %d venues; raise limit or narrow radius_km.", len(out.Venues), page.TotalHits)
	}

	return nil, out, nil
}

// venuesByName matches venue names near the center; ClassPass returns at most a handful of matches.
func venuesByName(ctx context.Context, cp *classpass.Client, name string, c center, out searchVenuesOutput) (searchVenuesOutput, error) {
	_, venues, err := cp.Keywords(ctx, name, c.Lat, c.Lon)
	if err != nil {
		return searchVenuesOutput{}, err
	}

	for _, v := range venues {
		out.Venues = append(out.Venues, foundVenue{
			VenueID:    v.ID,
			Name:       strings.TrimSpace(v.Name + " " + v.Subtitle),
			DistanceKm: v.DistanceKm,
		})
	}

	if len(out.Venues) == 0 {
		out.Note = fmt.Sprintf("No venue named like %q. Try activity or tag_id to browse venues by kind.", name)
	}

	return out, nil
}
