package server

import (
	"context"

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
