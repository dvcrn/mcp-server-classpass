package server

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/dvcrn/mcp-server-classpass/classpass"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type venueInput struct {
	VenueID int64 `json:"venue_id" jsonschema:"venue id from search, bookmarks, or reservations"`
}

type offering struct {
	ClassID         int64    `json:"class_id"`
	Name            string   `json:"name"`
	Activities      []string `json:"activities"`
	Level           string   `json:"level"`
	Description     string   `json:"description"`
	CreditsFrom     *int     `json:"credits_from"`
	DurationMinutes int      `json:"duration_minutes"`
}

type venueOutput struct {
	VenueID       int64      `json:"venue_id"`
	Name          string     `json:"name"`
	Subtitle      string     `json:"subtitle"`
	Tags          []string   `json:"tags"`
	About         string     `json:"about"`
	HowToGetThere string     `json:"how_to_get_there"`
	Rating        float64    `json:"rating"`
	RatingsCount  string     `json:"ratings_count"`
	Reviews       []review   `json:"reviews"`
	Classes       []offering `json:"classes"`
}

type review struct {
	Class string `json:"class"`
	Text  string `json:"text"`
}

func (h *handlers) getVenue(ctx context.Context, _ *mcp.CallToolRequest, in venueInput) (*mcp.CallToolResult, venueOutput, error) {
	v, err := h.cp.VenueDetail(ctx, in.VenueID)
	if err != nil {
		return nil, venueOutput{}, err
	}

	offerings, err := h.cp.VenueOfferings(ctx, in.VenueID)
	if err != nil {
		return nil, venueOutput{}, err
	}

	out := venueOutput{
		VenueID:       in.VenueID,
		Name:          v.Name,
		Subtitle:      v.Subtitle,
		Tags:          v.Tags,
		About:         v.About,
		HowToGetThere: v.HowToGetThere,
		Rating:        v.Rating,
		RatingsCount:  v.RatingsCount,
		Reviews:       []review{},
		Classes:       []offering{},
	}

	for _, r := range v.Reviews {
		out.Reviews = append(out.Reviews, review{Class: r.Class, Text: r.Text})
	}

	for _, o := range offerings {
		out.Classes = append(out.Classes, offering{
			ClassID:         o.ID,
			Name:            o.Name,
			Activities:      o.Activities,
			Level:           o.Level,
			Description:     o.Description,
			CreditsFrom:     o.CreditsFrom,
			DurationMinutes: o.DurationMinutes,
		})
	}

	return nil, out, nil
}

type searchVenuesInput struct {
	Query   string   `json:"query" jsonschema:"venue or studio name, e.g. B+TREE"`
	NearLat *float64 `json:"near_lat,omitempty" jsonschema:"latitude to rank nearby venues first; defaults to the user's ClassPass city"`
	NearLon *float64 `json:"near_lon,omitempty" jsonschema:"longitude to rank nearby venues first; defaults to the user's ClassPass city"`
}

type venueMatch struct {
	VenueID    int64   `json:"venue_id"`
	Name       string  `json:"name"`
	DistanceKm float64 `json:"distance_km"`
}

type searchVenuesOutput struct {
	Venues []venueMatch `json:"venues"`
}

func (h *handlers) searchVenues(ctx context.Context, _ *mcp.CallToolRequest, in searchVenuesInput) (*mcp.CallToolResult, searchVenuesOutput, error) {
	if in.Query == "" {
		return nil, searchVenuesOutput{}, errors.New("query is required")
	}

	if (in.NearLat == nil) != (in.NearLon == nil) {
		return nil, searchVenuesOutput{}, errors.New("near_lat and near_lon must be given together")
	}

	var near classpass.LatLon
	if in.NearLat != nil {
		near = classpass.LatLon{Lat: *in.NearLat, Lon: *in.NearLon}
	} else {
		home, err := h.cp.HomeLocation(ctx)
		if err != nil {
			return nil, searchVenuesOutput{}, fmt.Errorf("pass near_lat and near_lon; the user's ClassPass city is unavailable: %w", err)
		}

		near = home.LatLon
	}

	_, venues, err := h.cp.Keywords(ctx, in.Query, near.Lat, near.Lon)
	if err != nil {
		return nil, searchVenuesOutput{}, err
	}

	out := searchVenuesOutput{Venues: []venueMatch{}}
	for _, v := range venues {
		name := strings.TrimSpace(v.Name + " " + v.Subtitle)
		out.Venues = append(out.Venues, venueMatch{VenueID: v.ID, Name: name, DistanceKm: v.DistanceKm})
	}

	return nil, out, nil
}
