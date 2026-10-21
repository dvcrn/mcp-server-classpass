package server

import (
	"context"
	"errors"
	"fmt"

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
		near, _ = cp.HomeLocation(ctx)
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
		candidates = append(candidates, placeCandidate{Description: p.Description, PlaceID: p.PlaceID, Types: p.Types})
	}

	return candidates, nil
}
