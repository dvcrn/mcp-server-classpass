package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/dvcrn/mcp-server-classpass/classpass"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// connect starts the server against a fake ClassPass API and returns a client session.
func connect(t *testing.T, api http.HandlerFunc) *mcp.ClientSession {
	t.Helper()

	srv := httptest.NewServer(api)
	t.Cleanup(srv.Close)

	cp := classpass.New("tok", "", "")
	cp.BaseURL = srv.URL

	ctx := context.Background()
	st, ct := mcp.NewInMemoryTransports()
	if _, err := New(cp).Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}

	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })

	return cs
}

func TestToolsRegistered(t *testing.T) {
	cs := connect(t, func(w http.ResponseWriter, r *http.Request) {})

	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}

	var names []string
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
	}

	slices.Sort(names)
	want := []string{"add_bookmark", "book_slot", "cancel_reservation", "get_account", "get_slot", "get_venue", "list_bookmarks", "list_reservations", "list_slots", "list_tags", "remove_bookmark", "search_classes", "search_locations", "search_venues"}
	if !slices.Equal(names, want) {
		t.Errorf("tools = %v, want %v", names, want)
	}
}

func TestBookReservesWithModalPrice(t *testing.T) {
	var reserved map[string]any
	cs := connect(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/bff/v2/reservation_modal/42":
			io.WriteString(w, `{"state":0,"modal":{"policy":{"explainers":["Cancel 12 hours in advance."]},
				"pricing":{"credits":23},"footer":{"cta":[{"type":"credits","enabled":true}]}}}`)

		case "/v1/me/profile":
			io.WriteString(w, `{"id":5}`)

		case "/v3/lifecycle/user/5/balance":
			io.WriteString(w, `{"data":{"credit_balance":{"credits_remaining":321}}}`)

		case "/bff/v1/reservation_modal/42/reserve":
			if r.Method != http.MethodPost {
				t.Errorf("reserve method = %s", r.Method)
			}

			json.NewDecoder(r.Body).Decode(&reserved)
			io.WriteString(w, `{"state":0,"reservation_id":7,"credits_balance":{"credits_total":400,"credits_remaining":298}}`)

		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	})

	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "book_slot", Arguments: map[string]any{"slot_id": 42}})
	if err != nil {
		t.Fatal(err)
	}

	if res.IsError {
		t.Fatalf("tool error: %v", res.Content)
	}

	if reserved["credits"] != float64(23) || reserved["type"] != "credits" {
		t.Errorf("reserve body = %v", reserved)
	}

	b, _ := json.Marshal(res.StructuredContent)
	var out bookOutput
	json.Unmarshal(b, &out)

	want := bookOutput{ReservationID: 7, CreditsCharged: 23, CreditsRemaining: 298, CancellationPolicy: "Cancel 12 hours in advance."}
	if out != want {
		t.Errorf("output = %+v, want %+v", out, want)
	}
}

func TestBookRefusesWhenCreditsDisabled(t *testing.T) {
	cs := connect(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/bff/v2/reservation_modal/42" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}

		io.WriteString(w, `{"state":0,"modal":{"pricing":{"credits":23},"footer":{"cta":[{"type":"credits","enabled":false}]}}}`)
	})

	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "book_slot", Arguments: map[string]any{"slot_id": 42}})
	if err != nil {
		t.Fatal(err)
	}

	if !res.IsError {
		t.Error("expected tool error")
	}
}

func TestBookRefusesWhenBalanceTooLow(t *testing.T) {
	cs := connect(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/bff/v2/reservation_modal/42":
			io.WriteString(w, `{"state":0,"modal":{"pricing":{"credits":23},"footer":{"cta":[{"type":"credits","enabled":true}]}}}`)

		case "/v1/me/profile":
			io.WriteString(w, `{"id":5}`)

		case "/v3/lifecycle/user/5/balance":
			io.WriteString(w, `{"data":{"credit_balance":{"credits_remaining":10}}}`)

		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	})

	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "book_slot", Arguments: map[string]any{"slot_id": 42}})
	if err != nil {
		t.Fatal(err)
	}

	if !res.IsError {
		t.Error("expected tool error")
	}
}

// fakeSearchAPI serves one search page of three venues at different distances; cursor "next" returns an empty last page.
func fakeSearchAPI(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			SearchRequest struct {
				Cursor string `json:"cursor"`
			} `json:"search_request"`
		}
		json.NewDecoder(r.Body).Decode(&body)

		if body.SearchRequest.Cursor == "next" {
			io.WriteString(w, `{"nested_search":{"venues":[]}}`)
			return
		}

		// 1792630800 is 10:00 and 1792648800 is 15:00 in Asia/Tokyo.
		io.WriteString(w, `{"nested_search":{"cursor":"next","venues":[
			{"id":1,"name":"Far","tz":"Asia/Tokyo","distance":2.5,"classes":[{"id":10,"name":"A","schedules":[
				{"id":100,"starttime":1792630800,"endtime":1792634400,"availability":{"credits":10}}]}]},
			{"id":2,"name":"Near","tz":"Asia/Tokyo","distance":0.3,"classes":[{"id":20,"name":"B","schedules":[
				{"id":200,"starttime":1792630800,"endtime":1792634400,"availability":{"credits":30}},
				{"id":201,"starttime":1792648800,"endtime":1792652400,"availability":{"credits":12}}]}]},
			{"id":3,"name":"Empty","tz":"Asia/Tokyo","distance":0.1,"classes":[]}]}}`)
	}
}

func callSearch(t *testing.T, cs *mcp.ClientSession, args map[string]any) searchOutput {
	t.Helper()

	args["lat"], args["lon"], args["date"] = 35.66, 139.70, "2026-10-22"
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "search_classes", Arguments: args})
	if err != nil {
		t.Fatal(err)
	}

	if res.IsError {
		t.Fatalf("tool error: %v", res.Content)
	}

	b, _ := json.Marshal(res.StructuredContent)
	var out searchOutput
	json.Unmarshal(b, &out)

	return out
}

func TestSearchSortsFiltersAndPages(t *testing.T) {
	cs := connect(t, fakeSearchAPI(t))

	out := callSearch(t, cs, map[string]any{"limit": 1})
	if len(out.Venues) != 1 || out.Venues[0].VenueID != 2 || out.Cursor == "" {
		t.Fatalf("first page = %+v", out)
	}

	out = callSearch(t, cs, map[string]any{"limit": 1, "cursor": out.Cursor})
	if len(out.Venues) != 1 || out.Venues[0].VenueID != 1 || out.Cursor == "" {
		t.Fatalf("second page = %+v", out)
	}

	out = callSearch(t, cs, map[string]any{"limit": 1, "cursor": out.Cursor})
	if len(out.Venues) != 0 || out.Cursor != "" || out.Note == "" {
		t.Fatalf("last page = %+v", out)
	}
}

func TestSearchSlotFilters(t *testing.T) {
	cs := connect(t, fakeSearchAPI(t))

	out := callSearch(t, cs, map[string]any{"max_credits": 15, "time_after": "12:00"})
	if len(out.Venues) != 1 || out.Venues[0].VenueID != 2 {
		t.Fatalf("venues = %+v", out.Venues)
	}

	if slots := out.Venues[0].Classes[0].Slots; len(slots) != 1 || slots[0].SlotID != 201 {
		t.Errorf("slots = %+v", slots)
	}

	out = callSearch(t, cs, map[string]any{"slots_per_class": 1})
	for _, v := range out.Venues {
		if v.VenueID == 2 && (len(v.Classes[0].Slots) != 1 || v.Classes[0].MoreSlots != 1) {
			t.Errorf("capped class = %+v", v.Classes[0])
		}
	}
}

func TestSearchRadius(t *testing.T) {
	cs := connect(t, fakeSearchAPI(t))

	if out := callSearch(t, cs, map[string]any{}); out.RadiusKm != defaultRadiusKm {
		t.Errorf("default radius = %v", out.RadiusKm)
	}

	if out := callSearch(t, cs, map[string]any{"radius_km": 10}); out.RadiusKm != 10 {
		t.Errorf("explicit radius = %v", out.RadiusKm)
	}
}

// fakeCancelAPI serves one upcoming reservation (id 7, schedule 42) whose cancel modal shows message.
func fakeCancelAPI(t *testing.T, message string, cancelled *bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/bff/v1/users/me/bookings":
			io.WriteString(w, `{"bookings":[{"reservation_data":{"reservation_id":7,"raw_reservation":{"schedule_id":42}}}]}`)

		case "/bff/v1/reservation_modal/42/cancel":
			io.WriteString(w, `{"state":0,"reservation_id":7,"modal":{"message":`+message+`,
				"footer":{"cta":[{"type":"cancel","enabled":true}]}}}`)

		case "/v1/reservations/7/cancel":
			if r.Method != http.MethodPost {
				t.Errorf("cancel method = %s", r.Method)
			}

			*cancelled = true
			io.WriteString(w, `{"status":"CANCELLED","late_cancelled":false,"fee":"NONE","title":"Your reservation is cancelled"}`)

		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}
}

func callCancel(t *testing.T, cs *mcp.ClientSession, args map[string]any) *mcp.CallToolResult {
	t.Helper()

	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "cancel_reservation", Arguments: args})
	if err != nil {
		t.Fatal(err)
	}

	return res
}

func TestCancelFree(t *testing.T) {
	var cancelled bool
	cs := connect(t, fakeCancelAPI(t, "null", &cancelled))

	res := callCancel(t, cs, map[string]any{"reservation_id": 7})
	if res.IsError || !cancelled {
		t.Fatalf("error = %v, cancelled = %v, content = %v", res.IsError, cancelled, res.Content)
	}
}

func TestCancelLateNeedsConfirmation(t *testing.T) {
	var cancelled bool
	cs := connect(t, fakeCancelAPI(t, `"You will lose your credits."`, &cancelled))

	res := callCancel(t, cs, map[string]any{"reservation_id": 7})
	if !res.IsError || cancelled {
		t.Fatalf("unconfirmed late cancel: error = %v, cancelled = %v", res.IsError, cancelled)
	}

	res = callCancel(t, cs, map[string]any{"reservation_id": 7, "confirm_late": true})
	if res.IsError || !cancelled {
		t.Fatalf("confirmed late cancel: error = %v, cancelled = %v", res.IsError, cancelled)
	}
}

func TestCancelUnknownReservation(t *testing.T) {
	var cancelled bool
	cs := connect(t, fakeCancelAPI(t, "null", &cancelled))

	if res := callCancel(t, cs, map[string]any{"reservation_id": 99}); !res.IsError || cancelled {
		t.Fatalf("error = %v, cancelled = %v", res.IsError, cancelled)
	}
}

func TestSearchPlaceCandidates(t *testing.T) {
	var resolved []string
	cs := connect(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/unisearch/v1/location/autocomplete":
			io.WriteString(w, `{"data":{"predictions":[{"description":"Tokyo Chofu","place_id":"A","types":["locality"]},{"description":"Tokyo, Japan","place_id":"B","types":["locality"]}]}}`)

		case r.URL.Path == "/v1/me/profile":
			io.WriteString(w, `{"id":5}`)

		case r.URL.Path == "/v1/gls/user/5/location":
			io.WriteString(w, `{"location":{"centroid":{"latitude":35.6,"longitude":139.6}}}`)

		case strings.HasPrefix(r.URL.Path, "/unisearch/v1/users/5/locations/"):
			id := strings.TrimPrefix(r.URL.Path, "/unisearch/v1/users/5/locations/")
			resolved = append(resolved, id)
			io.WriteString(w, `{"data":{"location_history_item":{"formatted_address":"place `+id+`","lat":35.6,"lon":139.7,"calculated_radius_km":20}}}`)

		case r.URL.Path == "/unisearch/v3/layout/search":
			io.WriteString(w, `{"nested_search":{"venues":[]}}`)

		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	})

	search := func(args map[string]any) searchOutput {
		res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "search_classes", Arguments: args})
		if err != nil || res.IsError {
			t.Fatalf("err = %v, result = %v", err, res)
		}

		b, _ := json.Marshal(res.StructuredContent)
		var out searchOutput
		json.Unmarshal(b, &out)

		return out
	}

	out := search(map[string]any{"location": "Tokyo"})
	if out.PlaceID != "A" || len(out.OtherLocations) != 1 || out.OtherLocations[0].PlaceID != "B" || out.RadiusKm != 20 {
		t.Fatalf("by name = %+v", out)
	}

	out = search(map[string]any{"place_id": "B"})
	if out.PlaceID != "B" || out.Location != "place B" || len(out.OtherLocations) != 0 {
		t.Fatalf("by place_id = %+v", out)
	}

	if !slices.Equal(resolved, []string{"A", "B"}) {
		t.Errorf("resolved = %v", resolved)
	}
}

func TestSearchLocationsNear(t *testing.T) {
	var nears []map[string]float64
	cs := connect(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/me/profile":
			io.WriteString(w, `{"id":5}`)

		case "/v1/gls/user/5/location":
			io.WriteString(w, `{"location":{"centroid":{"latitude":35.6,"longitude":139.6}}}`)

		case "/unisearch/v1/location/autocomplete":
			var body struct {
				Near map[string]float64 `json:"near"`
			}
			json.NewDecoder(r.Body).Decode(&body)
			nears = append(nears, body.Near)

			io.WriteString(w, `{"data":{"predictions":[{"description":"Shibuya, Tokyo","place_id":"S","types":["locality","political"]}]}}`)

		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	})

	call := func(args map[string]any) locationsOutput {
		res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "search_locations", Arguments: args})
		if err != nil || res.IsError {
			t.Fatalf("err = %v, result = %v", err, res)
		}

		b, _ := json.Marshal(res.StructuredContent)
		var out locationsOutput
		json.Unmarshal(b, &out)

		return out
	}

	out := call(map[string]any{"query": "Shibuya"})
	if len(out.Locations) != 1 || out.Locations[0].PlaceID != "S" || !slices.Equal(out.Locations[0].Types, []string{"locality", "political"}) {
		t.Fatalf("locations = %+v", out)
	}

	call(map[string]any{"query": "Shibuya", "near_lat": 52.5, "near_lon": 13.4})

	want := []map[string]float64{{"lat": 35.6, "lon": 139.6}, {"lat": 52.5, "lon": 13.4}}
	if len(nears) != 2 || nears[0]["lat"] != want[0]["lat"] || nears[1]["lon"] != want[1]["lon"] {
		t.Errorf("near sent = %v, want %v", nears, want)
	}
}

// fakeBookmarksAPI serves one bookmark (venue 9, bookmark 900) and records add and remove calls.
func fakeBookmarksAPI(t *testing.T, added *[]int64, removed *[]string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/me/bookmarks":
			io.WriteString(w, `{"bookmarks":[{"type":"venue","venue":{"id":9,"bookmark_id":900,"name":"V"}}]}`)

		case r.Method == http.MethodPost && r.URL.Path == "/v1/me/bookmarks":
			var body struct {
				RefID int64  `json:"ref_id"`
				Type  string `json:"type"`
			}
			json.NewDecoder(r.Body).Decode(&body)

			if body.Type != "venue" {
				t.Errorf("type = %q", body.Type)
			}

			*added = append(*added, body.RefID)
			io.WriteString(w, `{"id":901}`)

		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/v1/me/bookmarks/"):
			*removed = append(*removed, strings.TrimPrefix(r.URL.Path, "/v1/me/bookmarks/"))
			w.WriteHeader(http.StatusNoContent)

		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}
}

func TestBookmarks(t *testing.T) {
	var added []int64
	var removed []string
	cs := connect(t, fakeBookmarksAPI(t, &added, &removed))

	call := func(name string, venueID int64) *mcp.CallToolResult {
		res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: map[string]any{"venue_id": venueID}})
		if err != nil {
			t.Fatal(err)
		}

		return res
	}

	if res := call("add_bookmark", 9); res.IsError {
		t.Fatalf("re-adding existing bookmark: %v", res.Content)
	}

	if res := call("add_bookmark", 5); res.IsError {
		t.Fatalf("adding bookmark: %v", res.Content)
	}

	if !slices.Equal(added, []int64{5}) {
		t.Errorf("added = %v, want only venue 5", added)
	}

	if res := call("remove_bookmark", 9); res.IsError {
		t.Fatalf("removing bookmark: %v", res.Content)
	}

	if res := call("remove_bookmark", 5); !res.IsError {
		t.Error("removing a venue that is not bookmarked should fail")
	}

	if !slices.Equal(removed, []string{"900"}) {
		t.Errorf("removed = %v, want bookmark 900", removed)
	}
}

// slotJSON renders one venue_schedule_list item; day is days after 2026-10-22 and hour is Asia/Tokyo local.
func slotJSON(id int64, classID int64, day, hour int) string {
	start := 1792630800 + day*86400 + (hour-10)*3600

	return fmt.Sprintf(`{"id":%d,"starttime":%d,"endtime":%d,"availability":{"credits":%d,"status":"available"},
		"class":{"id":%d,"name":"Class %d"},"venue":{"tz":"Asia/Tokyo"}}`, id, start, start+3600, 10+hour, classID, classID)
}

// fakeSlotsAPI serves class 10 with slots on Oct 22 to 26 across two pages, class 20 with none, and offerings of n classes.
func fakeSlotsAPI(t *testing.T, offerings int, requests *[]string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/bff/v1/venue_offerings/1":
			var classes []string
			for i := range offerings {
				classes = append(classes, fmt.Sprintf(`{"id":%d,"name":"Class %d"}`, (i+1)*10, (i+1)*10))
			}

			io.WriteString(w, `{"section_list":[{"class_list":[`+strings.Join(classes, ",")+`]}]}`)

		case "/bff/v1/venue_schedule_list/1":
			q := r.URL.Query()
			*requests = append(*requests, q.Get("class_id")+"@"+q.Get("date"))

			var items []string
			switch q.Get("class_id") + "@" + q.Get("date") {
			case "10@2026-10-22":
				items = []string{slotJSON(1, 10, 0, 10), slotJSON(2, 10, 1, 10), slotJSON(3, 10, 1, 15)}

			case "10@2026-10-23":
				items = []string{slotJSON(2, 10, 1, 10), slotJSON(3, 10, 1, 15), slotJSON(4, 10, 2, 10), slotJSON(5, 10, 4, 10)}
			}

			io.WriteString(w, `{"schedule_list":[`+strings.Join(items, ",")+`]}`)

		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}
}

func callTool[T any](t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) (T, *mcp.CallToolResult) {
	t.Helper()

	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatal(err)
	}

	var out T
	b, _ := json.Marshal(res.StructuredContent)
	json.Unmarshal(b, &out)

	return out, res
}

func slotIDs(c class) []int64 {
	var ids []int64
	for _, s := range c.Slots {
		ids = append(ids, s.SlotID)
	}

	return ids
}

func TestListSlotsPagesWithinRange(t *testing.T) {
	var requests []string
	cs := connect(t, fakeSlotsAPI(t, 0, &requests))

	out, res := callTool[listSlotsOutput](t, cs, "list_slots", map[string]any{"venue_id": 1, "class_id": 10, "date": "2026-10-22"})
	if res.IsError {
		t.Fatalf("tool error: %v", res.Content)
	}

	if out.EndDate != "2026-10-25" || len(out.Classes) != 1 || !slices.Equal(slotIDs(out.Classes[0]), []int64{1, 2, 3, 4}) {
		t.Fatalf("output = %+v", out)
	}

	if out.Classes[0].Name != "Class 10" {
		t.Errorf("class name = %q", out.Classes[0].Name)
	}

	if !slices.Equal(requests, []string{"10@2026-10-22", "10@2026-10-23"}) {
		t.Errorf("requests = %v", requests)
	}

	out, _ = callTool[listSlotsOutput](t, cs, "list_slots", map[string]any{"venue_id": 1, "class_id": 10, "date": "2026-10-22", "time_after": "12:00"})
	if len(out.Classes) != 1 || !slices.Equal(slotIDs(out.Classes[0]), []int64{3}) {
		t.Errorf("filtered = %+v", out)
	}
}

func TestListSlotsWholeVenue(t *testing.T) {
	var requests []string
	cs := connect(t, fakeSlotsAPI(t, 2, &requests))

	out, res := callTool[listSlotsOutput](t, cs, "list_slots", map[string]any{"venue_id": 1, "date": "2026-10-22"})
	if res.IsError {
		t.Fatalf("tool error: %v", res.Content)
	}

	// Without class_id the range defaults to the single start day, and classes without slots are dropped.
	if out.EndDate != "2026-10-22" || len(out.Classes) != 1 || !slices.Equal(slotIDs(out.Classes[0]), []int64{1}) {
		t.Fatalf("output = %+v", out)
	}

	if !slices.Contains(requests, "20@2026-10-22") {
		t.Errorf("class 20 was not fetched: %v", requests)
	}
}

func TestListSlotsLargeVenueReturnsChoices(t *testing.T) {
	var requests []string
	cs := connect(t, fakeSlotsAPI(t, maxVenueClasses+1, &requests))

	out, res := callTool[listSlotsOutput](t, cs, "list_slots", map[string]any{"venue_id": 1, "date": "2026-10-22"})
	if res.IsError {
		t.Fatalf("tool error: %v", res.Content)
	}

	if len(out.ClassChoices) != maxVenueClasses+1 || len(out.Classes) != 0 || out.Note == "" {
		t.Errorf("output = %+v", out)
	}

	if len(requests) != 0 {
		t.Errorf("fetched schedules anyway: %v", requests)
	}
}

func TestDateRangeCap(t *testing.T) {
	days, err := dateRange("2026-10-22", "2026-10-25")
	if err != nil || len(days) != 4 || days[3] != "2026-10-25" {
		t.Errorf("4-day range = %v, %v", days, err)
	}

	if _, err := dateRange("2026-10-22", "2026-10-26"); err == nil {
		t.Error("5-day range should fail")
	}

	if _, err := dateRange("2026-10-22", "2026-10-21"); err == nil {
		t.Error("end before start should fail")
	}
}

func TestSearchMultiDayMerges(t *testing.T) {
	var dates []string
	cs := connect(t, func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			SearchRequest struct {
				Filters struct {
					Date string `json:"date"`
				} `json:"filters"`
			} `json:"search_request"`
		}
		json.NewDecoder(r.Body).Decode(&body)

		d := body.SearchRequest.Filters.Date
		dates = append(dates, d)

		// Each day lists that day's slot plus the shared slot 100 that the merge must not duplicate.
		day := map[string]int{"2026-10-22": 0, "2026-10-23": 1}[d]
		start := 1792630800 + day*86400
		io.WriteString(w, fmt.Sprintf(`{"nested_search":{"cursor":"more","venues":[{"id":1,"name":"V","tz":"Asia/Tokyo","classes":[
			{"id":10,"name":"A","schedules":[{"id":%d,"starttime":%d,"endtime":%d},{"id":100,"starttime":1792630800,"endtime":1792634400}]}]}]}}`,
			200+day, start, start+3600))
	})

	out := callSearch(t, cs, map[string]any{"end_date": "2026-10-23"})
	if !slices.Equal(dates, []string{"2026-10-22", "2026-10-23"}) {
		t.Errorf("searched dates = %v", dates)
	}

	if out.EndDate != "2026-10-23" || len(out.Venues) != 1 || len(out.Venues[0].Classes) != 1 {
		t.Fatalf("output = %+v", out)
	}

	if ids := slotIDs(out.Venues[0].Classes[0]); !slices.Equal(ids, []int64{200, 100, 201}) {
		t.Errorf("merged slots = %v", ids)
	}

	if out.Cursor != "" || out.Note == "" {
		t.Errorf("multi-day search should note more results instead of returning cursor: %+v", out)
	}
}

func TestOptionalNumbersNotNullable(t *testing.T) {
	cs := connect(t, func(w http.ResponseWriter, r *http.Request) {})

	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}

	for _, tool := range res.Tools {
		b, _ := json.Marshal(tool.InputSchema)
		if strings.Contains(string(b), `"null"`) {
			t.Errorf("%s input schema declares null: %s", tool.Name, b)
		}
	}
}

// homeHandler serves the profile and ClassPass city lookups; other paths go to next.
func homeHandler(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/me/profile":
			io.WriteString(w, `{"id":5}`)

		case "/v1/gls/user/5/location":
			io.WriteString(w, `{"location":{"name":"Setagaya City","timezone":"Asia/Tokyo","centroid":{"latitude":35.64,"longitude":139.65}}}`)

		default:
			next(w, r)
		}
	}
}

const keywordsJSON = `{"data":{"results":[
	{"type":"activities","entries":[{"id":"activity-599","name":"Free"},{"id":"1145","name":"Massage"}]},
	{"type":"venues","entries":[{"id":282110,"name":"B+TREE","subtitle":"Sangenjaya","distance":1.1}]}]}}`

func TestSearchClassesVenueNameAndHomeDefault(t *testing.T) {
	var searched []map[string]any
	cs := connect(t, homeHandler(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/unisearch/v1/keyword/autocomplete":
			io.WriteString(w, keywordsJSON)

		case "/bff/v1/venue_offerings/282110":
			w.WriteHeader(http.StatusNotFound)

		case "/unisearch/v1/venues":
			io.WriteString(w, `{"total_hits":4,"venues":[]}`)

		case "/unisearch/v3/layout/search":
			var body struct {
				SearchRequest struct {
					Filters map[string]any `json:"filters"`
				} `json:"search_request"`
			}
			json.NewDecoder(r.Body).Decode(&body)
			searched = append(searched, body.SearchRequest.Filters)
			io.WriteString(w, `{"nested_search":{"venues":[]}}`)

		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))

	// A venue name must not silently match the unrelated first activity; it is answered as a venue instead.
	out, res := callTool[searchOutput](t, cs, "search_classes", map[string]any{"query": "B+TREE", "date": "2026-10-22"})
	if res.IsError {
		t.Fatalf("tool error: %v", res.Content)
	}

	if len(out.MatchedVenues) != 1 || out.MatchedVenues[0].VenueID != 282110 || out.Note == "" || len(searched) != 0 {
		t.Errorf("venue name output = %+v, searched = %v", out, searched)
	}

	out, res = callTool[searchOutput](t, cs, "search_classes", map[string]any{"query": "massage", "date": "2026-10-22"})
	if res.IsError {
		t.Fatalf("tool error: %v", res.Content)
	}

	if out.Activity != "Massage" || out.Location != "Setagaya City" {
		t.Errorf("output = %+v", out)
	}

	if len(searched) != 1 || searched[0]["lat"] != 35.64 {
		t.Errorf("searched = %v", searched)
	}

	if !strings.Contains(out.Note, "4 Massage venues") {
		t.Errorf("empty result note = %q", out.Note)
	}
}

func TestListReservationsIncludePast(t *testing.T) {
	var queries []string
	cs := connect(t, func(w http.ResponseWriter, r *http.Request) {
		queries = append(queries, r.URL.Query().Get("upcoming_only"))
		io.WriteString(w, `{"bookings":[]}`)
	})

	callTool[reservationsOutput](t, cs, "list_reservations", map[string]any{})
	callTool[reservationsOutput](t, cs, "list_reservations", map[string]any{"include_past": true})

	if !slices.Equal(queries, []string{"true", "false"}) {
		t.Errorf("upcoming_only = %v", queries)
	}
}

func TestBookRefusesPriceAboveMaxCredits(t *testing.T) {
	cs := connect(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/bff/v2/reservation_modal/42" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}

		io.WriteString(w, `{"state":0,"modal":{"pricing":{"credits":23},"footer":{"cta":[{"type":"credits","enabled":true}]}}}`)
	})

	if _, res := callTool[bookOutput](t, cs, "book_slot", map[string]any{"slot_id": 42, "max_credits": 20}); !res.IsError {
		t.Error("expected error when the price exceeds max_credits")
	}
}

const tagsJSON = `{"tags":[
	{"id":"21149","name":"Non-Cancellable","subtags":[]},
	{"id":"17142","name":"Marketing","subtags":[{"id":"1","name":"Promo"}]},
	{"id":"16585","name":"Food and Drink","subtags":[{"id":"19458","name":"Coffee","subtags":[]}]},
	{"id":"1143","name":"Experiences","subtags":[{"id":"1145","name":"Massage","subtags":[{"id":"9","name":"Deep tissue","subtags":[]}]}]}]}`

// fakeTagsAPI serves the tag tree, keyword lookups, and venue searches, recording the tags searched.
func fakeTagsAPI(t *testing.T, venueTags, classTags *[]string) http.HandlerFunc {
	return homeHandler(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/bff/v1/tags":
			io.WriteString(w, tagsJSON)

		case "/unisearch/v1/keyword/autocomplete":
			io.WriteString(w, `{"data":{"results":[{"type":"activities","entries":[{"id":"19458","name":"Coffee"}]},
				{"type":"venues","entries":[{"id":7,"name":"Cafe","distance":0.4}]}]}}`)

		case "/unisearch/v1/venues":
			var body struct {
				Tag []string `json:"tag"`
			}
			json.NewDecoder(r.Body).Decode(&body)
			*venueTags = append(*venueTags, body.Tag...)

			io.WriteString(w, `{"total_hits":3,"venues":[
				{"id":2,"name":"Far","distance":2.0,"is_open":true,"activities":"Coffee","rating_average":4.5,"rating_display_total":"10"},
				{"id":1,"name":"Near","distance":0.5,"is_open":false}]}`)

		case "/unisearch/v3/layout/search":
			var body struct {
				SearchRequest struct {
					Filters struct {
						Tag []string `json:"tag"`
					} `json:"filters"`
				} `json:"search_request"`
			}
			json.NewDecoder(r.Body).Decode(&body)
			*classTags = append(*classTags, body.SearchRequest.Filters.Tag...)

			io.WriteString(w, `{"nested_search":{"venues":[]}}`)

		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	})
}

func TestListTags(t *testing.T) {
	var venueTags, classTags []string
	cs := connect(t, fakeTagsAPI(t, &venueTags, &classTags))

	paths := func(out listTagsOutput) []string {
		var p []string
		for _, e := range out.Tags {
			p = append(p, e.TagID+" "+e.Path)
		}

		return p
	}

	out, _ := callTool[listTagsOutput](t, cs, "list_tags", map[string]any{})
	want := []string{"16585 Food and Drink", "19458 Food and Drink > Coffee", "1143 Experiences", "1145 Experiences > Massage"}
	if got := paths(out); !slices.Equal(got, want) {
		t.Errorf("overview = %v, want %v", got, want)
	}

	out, _ = callTool[listTagsOutput](t, cs, "list_tags", map[string]any{"category": "massage"})
	want = []string{"1145 Massage", "9 Massage > Deep tissue"}
	if got := paths(out); !slices.Equal(got, want) {
		t.Errorf("category = %v, want %v", got, want)
	}

	if _, res := callTool[listTagsOutput](t, cs, "list_tags", map[string]any{"category": "nope"}); !res.IsError {
		t.Error("unknown category should fail")
	}
}

func TestSearchVenuesModes(t *testing.T) {
	var venueTags, classTags []string
	cs := connect(t, fakeTagsAPI(t, &venueTags, &classTags))

	out, res := callTool[searchVenuesOutput](t, cs, "search_venues", map[string]any{"activity": "coffee"})
	if res.IsError {
		t.Fatalf("tool error: %v", res.Content)
	}

	if out.Tag != "Coffee" || out.TotalHits != 3 || len(out.Venues) != 2 || out.Venues[0].VenueID != 1 || out.Note == "" {
		t.Errorf("activity search = %+v", out)
	}

	if v := out.Venues[1]; v.OpenNow == nil || !*v.OpenNow || v.Rating != 4.5 || v.RatingsCount != "10" {
		t.Errorf("venue fields = %+v", v)
	}

	out, _ = callTool[searchVenuesOutput](t, cs, "search_venues", map[string]any{"tag_id": "16585"})
	if out.Tag != "Food and Drink" {
		t.Errorf("tag search = %+v", out)
	}

	if !slices.Equal(venueTags, []string{"19458", "16585"}) {
		t.Errorf("searched tags = %v", venueTags)
	}

	out, _ = callTool[searchVenuesOutput](t, cs, "search_venues", map[string]any{"name": "Cafe"})
	if len(out.Venues) != 1 || out.Venues[0].VenueID != 7 || out.Venues[0].OpenNow != nil {
		t.Errorf("name search = %+v", out)
	}

	for _, args := range []map[string]any{{}, {"name": "a", "tag_id": "1"}} {
		if _, res := callTool[searchVenuesOutput](t, cs, "search_venues", args); !res.IsError {
			t.Errorf("args %v should fail", args)
		}
	}

	if _, res := callTool[searchVenuesOutput](t, cs, "search_venues", map[string]any{"tag_id": "999"}); !res.IsError {
		t.Error("unknown tag_id should fail")
	}
}

func TestSearchClassesTagID(t *testing.T) {
	var venueTags, classTags []string
	cs := connect(t, fakeTagsAPI(t, &venueTags, &classTags))

	out := callSearch(t, cs, map[string]any{"tag_id": "1145"})
	if out.Activity != "Massage" || !slices.Equal(classTags, []string{"1145"}) {
		t.Errorf("activity = %q, searched tags = %v", out.Activity, classTags)
	}
}

func TestEmbeddedLocationArgsInSchema(t *testing.T) {
	cs := connect(t, func(w http.ResponseWriter, r *http.Request) {})

	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}

	for _, tool := range res.Tools {
		if tool.Name != "search_classes" && tool.Name != "search_venues" {
			continue
		}

		b, _ := json.Marshal(tool.InputSchema)
		for _, p := range []string{`"location"`, `"place_id"`, `"lat"`, `"radius_km"`} {
			if !strings.Contains(string(b), p) {
				t.Errorf("%s schema lacks %s: %s", tool.Name, p, b)
			}
		}
	}
}

func TestGetVenueWithoutClassList(t *testing.T) {
	cs := connect(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/bff/v2/venue_detail/9":
			io.WriteString(w, `{"items":[{"key":"header","header":{"title":"V"}}]}`)

		case "/bff/v1/venue_offerings/9":
			w.WriteHeader(http.StatusNotFound)

		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	})

	out, res := callTool[venueOutput](t, cs, "get_venue", map[string]any{"venue_id": 9})
	if res.IsError || out.Name != "V" || len(out.Classes) != 0 || out.Note == "" {
		t.Errorf("error = %v, output = %+v", res.IsError, out)
	}

	if _, res := callTool[listSlotsOutput](t, cs, "list_slots", map[string]any{"venue_id": 9, "date": "2026-10-22"}); !res.IsError {
		t.Error("list_slots without class_id should explain the missing class list")
	}
}

func TestSearchClassesSingleVenueSlots(t *testing.T) {
	var requests []string
	slots := fakeSlotsAPI(t, 1, &requests)
	cs := connect(t, homeHandler(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/unisearch/v1/keyword/autocomplete":
			io.WriteString(w, `{"data":{"results":[{"type":"venues","entries":[{"id":1,"name":"Studio","distance":0.3}]}]}}`)

		default:
			slots(w, r)
		}
	}))

	out, res := callTool[searchOutput](t, cs, "search_classes", map[string]any{"query": "Studio", "date": "2026-10-22", "slots_per_class": 1})
	if res.IsError {
		t.Fatalf("tool error: %v", res.Content)
	}

	if len(out.Venues) != 1 || out.Venues[0].VenueID != 1 || len(out.MatchedVenues) != 0 {
		t.Fatalf("output = %+v", out)
	}

	if c := out.Venues[0].Classes; len(c) != 1 || !slices.Equal(slotIDs(c[0]), []int64{1}) {
		t.Errorf("classes = %+v", c)
	}
}

func TestActivitySuggestsTags(t *testing.T) {
	cs := connect(t, homeHandler(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/bff/v1/tags":
			io.WriteString(w, tagsJSON)

		case "/unisearch/v1/keyword/autocomplete":
			io.WriteString(w, `{"data":{"results":[]}}`)

		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))

	_, res := callTool[searchOutput](t, cs, "search_classes", map[string]any{"query": "drinks", "date": "2026-10-22"})
	if !res.IsError {
		t.Fatal("expected error")
	}

	if msg := res.Content[0].(*mcp.TextContent).Text; !strings.Contains(msg, "Food and Drink (tag_id 16585)") {
		t.Errorf("error should suggest the tag: %s", msg)
	}
}

// fakeTokyoAPI serves place lookups where "Tokyo" matches only businesses, plus a home city of homeName.
func fakeTokyoAPI(t *testing.T, homeName string, resolved *[]string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/me/profile":
			io.WriteString(w, `{"id":5}`)

		case r.URL.Path == "/v1/gls/user/5/location":
			io.WriteString(w, `{"location":{"name":"`+homeName+`","timezone":"Asia/Tokyo","centroid":{"latitude":35.64,"longitude":139.65}}}`)

		case r.URL.Path == "/unisearch/v1/location/autocomplete":
			io.WriteString(w, `{"data":{"predictions":[
				{"description":"Keio Arena TOKYO, Chofu","main_text":"Keio Arena TOKYO","place_id":"ARENA","types":["stadium","establishment"]},
				{"description":"Tokyo Tower, Minato","main_text":"Tokyo Tower","place_id":"TOWER","types":["tourist_attraction","establishment"]}]}}`)

		case strings.HasPrefix(r.URL.Path, "/unisearch/v1/users/5/locations/"):
			*resolved = append(*resolved, strings.TrimPrefix(r.URL.Path, "/unisearch/v1/users/5/locations/"))
			io.WriteString(w, `{"data":{"location_history_item":{"formatted_address":"resolved","lat":35.6,"lon":139.7}}}`)

		case r.URL.Path == "/unisearch/v3/layout/search":
			io.WriteString(w, `{"nested_search":{"venues":[]}}`)

		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}
}

func TestCityNameFallsBackToHome(t *testing.T) {
	var resolved []string
	cs := connect(t, fakeTokyoAPI(t, "Setagaya City, Tokyo, Japan", &resolved))

	out, res := callTool[searchOutput](t, cs, "search_classes", map[string]any{"location": "Tokyo", "date": "2026-10-22"})
	if res.IsError {
		t.Fatalf("tool error: %v", res.Content)
	}

	if out.Location != "Setagaya City, Tokyo, Japan" || len(resolved) != 0 {
		t.Errorf("location = %q, resolved = %v", out.Location, resolved)
	}

	// A landmark named exactly like the query is still accepted.
	out, _ = callTool[searchOutput](t, cs, "search_classes", map[string]any{"location": "tokyo tower", "date": "2026-10-22"})
	if !slices.Equal(resolved, []string{"TOWER"}) {
		t.Errorf("landmark resolved = %v, output = %+v", resolved, out)
	}
}

func TestCityNameRefusedOutsideHome(t *testing.T) {
	var resolved []string
	cs := connect(t, fakeTokyoAPI(t, "Berlin, Germany", &resolved))

	_, res := callTool[searchOutput](t, cs, "search_classes", map[string]any{"location": "Tokyo", "date": "2026-10-22"})
	if !res.IsError || len(resolved) != 0 {
		t.Fatalf("error = %v, resolved = %v", res.IsError, resolved)
	}

	if msg := res.Content[0].(*mcp.TextContent).Text; !strings.Contains(msg, "place_id ARENA") {
		t.Errorf("error should list candidates: %s", msg)
	}
}
