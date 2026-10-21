package server

import (
	"time"

	"github.com/dvcrn/mcp-server-classpass/classpass"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// handlers holds the tool implementations, one method per tool.
type handlers struct {
	cp *classpass.Client
}

// New returns an MCP server exposing the ClassPass tools backed by cp.
func New(cp *classpass.Client) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "classpass", Version: "0.1.0"}, nil)
	tools(s, &handlers{cp: cp})

	return s
}

func tools(s *mcp.Server, h *handlers) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "me",
		Description: "Get the ClassPass account: name, plan, remaining credits, and upcoming reservation count.",
	}, h.me)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "reservations",
		Description: "List upcoming ClassPass reservations. Times are in the venue's local timezone.",
	}, h.reservations)

	mcp.AddTool(s, &mcp.Tool{
		Name: "search",
		Description: "Search ClassPass for an activity near a place on a given day. " +
			"Returns venues with their classes and bookable time slots (schedule_id, credits). Times are venue-local.",
	}, h.search)
}

func joinAddress(a classpass.Address) string {
	if a.City == "" {
		return a.AddressLine1
	}

	return a.AddressLine1 + ", " + a.City
}

// localTime converts an API UTC timestamp to the venue's timezone, falling back to the raw value.
func localTime(ts, tz string) string {
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return ts
	}

	loc, err := time.LoadLocation(tz)
	if err != nil {
		return ts
	}

	return t.In(loc).Format(time.RFC3339)
}
