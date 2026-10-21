package server

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

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
