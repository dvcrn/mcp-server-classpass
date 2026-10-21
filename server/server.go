package server

import (
	"slices"
	"time"

	"github.com/dvcrn/mcp-server-classpass/classpass"
	"github.com/google/jsonschema-go/jsonschema"
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
		Name:        "get_account",
		Description: "Get the user's ClassPass account: name, plan, remaining credit balance, and upcoming reservation count.",
		Annotations: readOnly,
	}, h.getAccount)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_reservations",
		Description: "List the user's upcoming reservations with reservation_id and slot_id, or past ones too with include_past. Times are venue-local.",
		Annotations: readOnly,
	}, h.listReservations)

	mcp.AddTool(s, &mcp.Tool{
		Name: "search_locations",
		Description: "Look up places by name and return candidates with place_id, best match first. " +
			"Pass the chosen place_id to search_classes. Types tell areas (sublocality, locality, transit_station) apart from shops (establishment). " +
			"Bare city names often match shops; prefer a ward or neighborhood, or pass lat/lon to search_classes.",
		Annotations: readOnly,
		InputSchema: inputSchema[locationsInput](),
	}, h.searchLocations)

	mcp.AddTool(s, &mcp.Tool{
		Name: "search_classes",
		Description: "Discover classes near a place over one to four days, optionally filtered by activity, start time window, and credit cost. " +
			"Defaults to the user's ClassPass city when no location is given. " +
			"Returns venues nearest first, each with classes and bookable time slots (slot_id, credits). Times are venue-local.",
		InputSchema: inputSchema[searchInput](),
	}, h.searchClasses)

	mcp.AddTool(s, &mcp.Tool{
		Name: "search_venues",
		Description: "Find venues by name, e.g. a studio the user mentions. Returns venue_id for get_venue and list_slots. " +
			"Use search_classes instead to search by activity.",
		Annotations: readOnly,
		InputSchema: inputSchema[searchVenuesInput](),
	}, h.searchVenues)

	mcp.AddTool(s, &mcp.Tool{
		Name: "get_venue",
		Description: "Get a venue's details, rating with a few recent reviews, and the classes it offers (class_id, credits_from). " +
			"Use list_slots for a class's time slots.",
		Annotations: readOnly,
	}, h.getVenue)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_slots",
		Description: "List upcoming time slots of one class at a known venue, from a start date across the next several days. Times are venue-local.",
		Annotations: readOnly,
		InputSchema: inputSchema[listSlotsInput](),
	}, h.listSlots)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_slot",
		Description: "Get details for one time slot: class description, venue, credits, availability, and cancellation policy.",
		Annotations: readOnly,
	}, h.getSlot)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_bookmarks",
		Description: "List the venues the user has bookmarked in ClassPass.",
		Annotations: readOnly,
	}, h.listBookmarks)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "add_bookmark",
		Description: "Bookmark a venue for the user. Does nothing if it is already bookmarked.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: new(false), IdempotentHint: true},
	}, h.addBookmark)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "remove_bookmark",
		Description: "Remove a venue from the user's bookmarks. Confirm with the user before calling.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: new(true), IdempotentHint: true},
	}, h.removeBookmark)

	mcp.AddTool(s, &mcp.Tool{
		Name: "book_slot",
		Description: "Book a time slot with credits. This spends credits on the user's account; " +
			"confirm the class, time, and credit cost with the user before calling, and pass that cost as max_credits. " +
			"Fails without booking if the price rose above max_credits or the user lacks the credits.",
		InputSchema: inputSchema[bookInput](),
		Annotations: &mcp.ToolAnnotations{DestructiveHint: new(false)},
	}, h.bookSlot)

	mcp.AddTool(s, &mcp.Tool{
		Name: "cancel_reservation",
		Description: "Cancel an upcoming reservation. Confirm with the user before calling. " +
			"If cancelling would forfeit credits or charge a fee, the call fails and returns ClassPass's warning; " +
			"ask the user for explicit confirmation before calling again with confirm_late=true.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: new(true)},
	}, h.cancelReservation)
}

var readOnly = &mcp.ToolAnnotations{ReadOnlyHint: true}

// inputSchema infers T's input schema and declares optional pointer fields with their plain type. Pointers
// distinguish "unset" from a meaningful zero, but the inferred ["null", type] reads as a different kind of
// parameter than the other optional fields.
func inputSchema[T any]() *jsonschema.Schema {
	s, err := jsonschema.For[T](nil)
	if err != nil {
		panic(err)
	}

	for _, p := range s.Properties {
		if len(p.Types) == 2 && slices.Contains(p.Types, "null") {
			p.Type = slices.DeleteFunc(slices.Clone(p.Types), func(t string) bool { return t == "null" })[0]
			p.Types = nil
		}
	}

	return s
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

// unixLocal formats a unix timestamp in the venue's timezone, falling back to UTC.
func unixLocal(sec int64, tz string) string {
	return time.Unix(sec, 0).In(venueLocation(tz)).Format(time.RFC3339)
}

func toSlot(s classpass.Schedule, tz string) slot {
	return slot{
		SlotID:          s.ID,
		Start:           unixLocal(s.StartTime, tz),
		End:             unixLocal(s.EndTime, tz),
		DurationMinutes: s.DurationMinutes,
		Credits:         s.Availability.Credits,
		Status:          s.Availability.Status,
	}
}
