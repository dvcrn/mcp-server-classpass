package server

import (
	"context"

	"github.com/dvcrn/mcp-server-classpass/classpass"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type bookmark struct {
	VenueID int64  `json:"venue_id"`
	Name    string `json:"name"`
	Address string `json:"address"`
}

type bookmarksOutput struct {
	Bookmarks []bookmark `json:"bookmarks"`
}

func (h *handlers) listBookmarks(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, bookmarksOutput, error) {
	venues, err := h.cp.Bookmarks(ctx)
	if err != nil {
		return nil, bookmarksOutput{}, err
	}

	out := bookmarksOutput{Bookmarks: []bookmark{}}
	for _, v := range venues {
		out.Bookmarks = append(out.Bookmarks, bookmark{VenueID: v.ID, Name: venueName(v.Name, v.Subtitle), Address: joinAddress(v.Address)})
	}

	return nil, out, nil
}

// findBookmark returns the user's bookmark for venueID, or nil if the venue is not bookmarked.
func (h *handlers) findBookmark(ctx context.Context, venueID int64) (*classpass.BookmarkedVenue, error) {
	venues, err := h.cp.Bookmarks(ctx)
	if err != nil {
		return nil, err
	}

	for i := range venues {
		if venues[i].ID == venueID {
			return &venues[i], nil
		}
	}

	return nil, nil
}
