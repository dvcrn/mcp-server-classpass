package server

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/dvcrn/mcp-server-classpass/classpass"
)

// defaultRadiusKm is the search radius around raw coordinates or the user's city; named places use at least this.
const defaultRadiusKm = 3.0

// locationArgs are the ways a search tool can pick its center, embedded in each tool's input.
type locationArgs struct {
	Location string   `json:"location,omitempty" jsonschema:"place name, e.g. Shibuya or Sangenjaya; defaults to the user's ClassPass city when no location, place_id, or lat and lon is given"`
	PlaceID  string   `json:"place_id,omitempty" jsonschema:"exact place from search_locations or a previous result's other_locations"`
	Lat      *float64 `json:"lat,omitempty" jsonschema:"latitude of the search center"`
	Lon      *float64 `json:"lon,omitempty" jsonschema:"longitude of the search center"`
	RadiusKm *float64 `json:"radius_km,omitempty" jsonschema:"search radius in km; defaults to 3, or the size of the named place if larger"`
}

// center is a resolved search center. Others are the runner-up matches when it came from a place name.
type center struct {
	Lat, Lon, RadiusKm float64
	Name, PlaceID      string
	Others             []placeCandidate
}

// resolveCenter picks the center from lat/lon, place_id, or a place name, in that order, defaulting to the
// user's ClassPass city. Resolving a place adds it to the user's ClassPass location history.
func resolveCenter(ctx context.Context, cp *classpass.Client, a locationArgs) (center, error) {
	if a.RadiusKm != nil && *a.RadiusKm <= 0 {
		return center{}, errors.New("radius_km must be positive")
	}

	c, err := baseCenter(ctx, cp, a)
	if err != nil {
		return center{}, err
	}

	if a.RadiusKm != nil {
		c.RadiusKm = *a.RadiusKm
	}

	return c, nil
}

func baseCenter(ctx context.Context, cp *classpass.Client, a locationArgs) (center, error) {
	c := center{RadiusKm: defaultRadiusKm}

	if a.Lat != nil && a.Lon != nil {
		c.Lat, c.Lon = *a.Lat, *a.Lon
		c.Name = fmt.Sprintf("%f,%f", c.Lat, c.Lon)

		return c, nil
	}

	placeID := a.PlaceID
	if placeID == "" && a.Location == "" {
		home, err := cp.HomeLocation(ctx)
		if err != nil {
			return center{}, fmt.Errorf("no location given and the user's ClassPass city is unavailable: %w", err)
		}

		c.Lat, c.Lon, c.Name = home.Lat, home.Lon, home.Name

		return c, nil
	}

	if placeID == "" {
		candidates, err := placeCandidates(ctx, cp, a.Location, nil)
		if err != nil {
			return center{}, err
		}

		i := pickPlace(candidates, a.Location)
		if i < 0 {
			return homeOrRefuse(ctx, cp, a.Location, candidates)
		}

		placeID = candidates[i].PlaceID
		c.Others = slices.Delete(slices.Clone(candidates), i, i+1)
	}

	uid, err := cp.UserID(ctx)
	if err != nil {
		return center{}, err
	}

	place, err := cp.ResolvePlace(ctx, uid, placeID)
	if err != nil {
		return center{}, err
	}

	c.Lat, c.Lon, c.Name, c.PlaceID = place.Lat, place.Lon, place.FormattedAddress, placeID
	c.RadiusKm = max(c.RadiusKm, place.RadiusKm)

	return c, nil
}

// homeOrRefuse handles a place name that only matched businesses. Bare city names do this (for "Tokyo", Google
// suggests an arena and a mall), so the user's ClassPass city is used when its name contains the query.
func homeOrRefuse(ctx context.Context, cp *classpass.Client, query string, candidates []placeCandidate) (center, error) {
	if home, err := cp.HomeLocation(ctx); err == nil {
		for _, part := range strings.Split(home.Name, ",") {
			if strings.EqualFold(strings.TrimSpace(part), strings.TrimSpace(query)) {
				return center{Lat: home.Lat, Lon: home.Lon, RadiusKm: defaultRadiusKm, Name: home.Name, Others: candidates}, nil
			}
		}
	}

	var names []string
	for _, c := range candidates {
		names = append(names, fmt.Sprintf("%s (place_id %s)", c.Description, c.PlaceID))
	}

	return center{}, fmt.Errorf("%q only matched specific businesses, not an area: %s. "+
		"Pass a ward or neighborhood as location, one of these as place_id, or lat and lon", query, strings.Join(names, "; "))
}
