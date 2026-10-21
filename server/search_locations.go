package server

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/dvcrn/mcp-server-classpass/classpass"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// maxPlaceCandidates caps how many place matches are returned for one lookup.
const maxPlaceCandidates = 5

type locationsInput struct {
	Query   string   `json:"query" jsonschema:"place name, e.g. Shibuya, Sangenjaya Station, Kreuzberg"`
	NearLat *float64 `json:"near_lat,omitempty" jsonschema:"latitude to rank nearby matches first; defaults to the user's ClassPass city"`
	NearLon *float64 `json:"near_lon,omitempty" jsonschema:"longitude to rank nearby matches first; defaults to the user's ClassPass city"`
}

type placeCandidate struct {
	Description string   `json:"description"`
	PlaceID     string   `json:"place_id"`
	Types       []string `json:"types,omitempty"`

	mainText string
}

type locationsOutput struct {
	Locations []placeCandidate `json:"locations"`
}

func (h *handlers) searchLocations(ctx context.Context, _ *mcp.CallToolRequest, in locationsInput) (*mcp.CallToolResult, locationsOutput, error) {
	if in.Query == "" {
		return nil, locationsOutput{}, errors.New("query is required")
	}

	if (in.NearLat == nil) != (in.NearLon == nil) {
		return nil, locationsOutput{}, errors.New("near_lat and near_lon must be given together")
	}

	var near *classpass.LatLon
	if in.NearLat != nil {
		near = &classpass.LatLon{Lat: *in.NearLat, Lon: *in.NearLon}
	}

	candidates, err := placeCandidates(ctx, h.cp, in.Query, near)
	if err != nil {
		return nil, locationsOutput{}, err
	}

	return nil, locationsOutput{Locations: candidates}, nil
}

// placeCandidates returns place matches for query, best first. A nil near falls back to the user's ClassPass city,
// which is how the app ranks matches; lookups still work without it if that call fails.
func placeCandidates(ctx context.Context, cp *classpass.Client, query string, near *classpass.LatLon) ([]placeCandidate, error) {
	if near == nil {
		if home, err := cp.HomeLocation(ctx); err == nil {
			near = &home.LatLon
		}
	}

	preds, err := cp.LocationAutocomplete(ctx, query, near)
	if err != nil {
		return nil, err
	}

	if len(preds) == 0 {
		return nil, fmt.Errorf("no place matches %q", query)
	}

	var candidates []placeCandidate
	for _, p := range preds[:min(len(preds), maxPlaceCandidates)] {
		candidates = append(candidates, placeCandidate{Description: p.Description, PlaceID: p.PlaceID, Types: p.Types, mainText: p.MainText})
	}

	return candidates, nil
}

// isArea reports whether Google place types describe an area or station rather than a single business.
func isArea(types []string) bool {
	for _, t := range types {
		switch {
		case t == "political", t == "locality", t == "neighborhood", t == "colloquial_area", t == "postal_code",
			t == "transit_station", t == "train_station", t == "subway_station",
			strings.HasPrefix(t, "sublocality"), strings.HasPrefix(t, "administrative_area"):
			return true
		}
	}

	return false
}

// pickPlace returns the index of the best candidate for query: the first area, or a place named exactly like the
// query (e.g. a landmark). It returns -1 when every candidate is an unrelated business, as for bare city names.
func pickPlace(candidates []placeCandidate, query string) int {
	for i, c := range candidates {
		if isArea(c.Types) || strings.EqualFold(strings.TrimSpace(c.mainText), strings.TrimSpace(query)) {
			return i
		}
	}

	return -1
}
