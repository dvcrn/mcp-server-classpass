package server

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type bookmarkInput struct {
	VenueID int64 `json:"venue_id" jsonschema:"venue id from search_classes, get_venue, or list_bookmarks"`
}

type bookmarkOutput struct {
	VenueID    int64 `json:"venue_id"`
	Bookmarked bool  `json:"bookmarked"`
}

// addBookmark is idempotent: an already bookmarked venue is left as is.
func (h *handlers) addBookmark(ctx context.Context, _ *mcp.CallToolRequest, in bookmarkInput) (*mcp.CallToolResult, bookmarkOutput, error) {
	existing, err := h.findBookmark(ctx, in.VenueID)
	if err != nil {
		return nil, bookmarkOutput{}, err
	}

	if existing == nil {
		if _, err := h.cp.AddBookmark(ctx, in.VenueID); err != nil {
			return nil, bookmarkOutput{}, err
		}
	}

	return nil, bookmarkOutput{VenueID: in.VenueID, Bookmarked: true}, nil
}
