package server

import (
	"context"
	"fmt"

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

type bookmarkInput struct {
	VenueID int64 `json:"venue_id" jsonschema:"venue id from search_classes, get_venue, or list_bookmarks"`
}

type bookmarkOutput struct {
	VenueID    int64 `json:"venue_id"`
	Bookmarked bool  `json:"bookmarked"`
}

func (h *handlers) listBookmarks(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, bookmarksOutput, error) {
	venues, err := h.cp.Bookmarks(ctx)
	if err != nil {
		return nil, bookmarksOutput{}, err
	}

	out := bookmarksOutput{Bookmarks: []bookmark{}}
	for _, v := range venues {
		name := v.Name
		if v.Subtitle != "" {
			name += " " + v.Subtitle
		}

		out.Bookmarks = append(out.Bookmarks, bookmark{VenueID: v.ID, Name: name, Address: joinAddress(v.Address)})
	}

	return nil, out, nil
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

func (h *handlers) removeBookmark(ctx context.Context, _ *mcp.CallToolRequest, in bookmarkInput) (*mcp.CallToolResult, bookmarkOutput, error) {
	existing, err := h.findBookmark(ctx, in.VenueID)
	if err != nil {
		return nil, bookmarkOutput{}, err
	}

	if existing == nil {
		return nil, bookmarkOutput{}, fmt.Errorf("venue %d is not bookmarked", in.VenueID)
	}

	if err := h.cp.RemoveBookmark(ctx, existing.BookmarkID); err != nil {
		return nil, bookmarkOutput{}, err
	}

	return nil, bookmarkOutput{VenueID: in.VenueID, Bookmarked: false}, nil
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
