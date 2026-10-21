package classpass

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func newTestClient(t *testing.T, token, user, pass string, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c := New(token, user, pass)
	c.BaseURL = srv.URL
	return c
}

func TestAppHeaders(t *testing.T) {
	c := newTestClient(t, "tok", "", "", func(w http.ResponseWriter, r *http.Request) {
		for k, want := range map[string]string{
			"Authorization": "Token tok",
			"App-Version":   appVersion,
			"User-Agent":    userAgent,
			"platform":      "ios",
			"X-CP-Device":   "iPad",
		} {
			if got := r.Header.Get(k); got != want {
				t.Errorf("%s = %q, want %q", k, got, want)
			}
		}
		if r.Header.Get("X-CP-Session-ID") == "" {
			t.Error("missing X-CP-Session-ID")
		}
		io.WriteString(w, `{"status":200,"data":{"credit_balance":{"credits_remaining":321}}}`)
	})
	got, err := c.CreditsRemaining(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if got != 321 {
		t.Errorf("credits = %d, want 321", got)
	}
}

func TestReloginOnUnauthorized(t *testing.T) {
	logins := 0
	c := newTestClient(t, "stale", "u@example.com", "pw", func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v2/auth/login":
			logins++
			var body map[string]string
			json.NewDecoder(r.Body).Decode(&body)
			if body["username"] != "u@example.com" || body["password"] != "pw" || body["client_id"] != loginClientID {
				t.Errorf("login body = %v", body)
			}
			io.WriteString(w, `{"token":"fresh"}`)
		case "/v1/me/profile":
			if r.Header.Get("Authorization") != "Token fresh" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			io.WriteString(w, `{"id":42,"first_name":"A"}`)
		}
	})
	p, err := c.Profile(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if p.ID != 42 || logins != 1 {
		t.Errorf("id = %d, logins = %d", p.ID, logins)
	}
	if id, _ := c.UserID(context.Background()); id != 42 {
		t.Errorf("cached user id = %d", id)
	}
}

func TestUnauthorizedWithoutCredentials(t *testing.T) {
	c := newTestClient(t, "stale", "", "", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	if _, err := c.Profile(context.Background()); err == nil {
		t.Fatal("expected error")
	}
}

func TestKeywordsDecodesActivitiesAndVenues(t *testing.T) {
	c := newTestClient(t, "tok", "", "", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"status":200,"data":{"results":[
			{"type":"full_text_search","entries":[{"type":"full_text_search_term","id":null}]},
			{"type":"activities","entries":[{"type":"activity","id":"1145","name":"Massage"}]},
			{"type":"venues","entries":[{"type":"venue","id":282110,"name":"B+TREE","subtitle":"Sangenjaya","distance":1.1}]}]}}`)
	})

	acts, venues, err := c.Keywords(context.Background(), "massage", 35.6, 139.6)
	if err != nil {
		t.Fatal(err)
	}

	if len(acts) != 1 || acts[0] != (Activity{ID: "1145", Name: "Massage"}) {
		t.Errorf("activities = %+v", acts)
	}

	if len(venues) != 1 || venues[0] != (VenueMatch{ID: 282110, Name: "B+TREE", Subtitle: "Sangenjaya", DistanceKm: 1.1}) {
		t.Errorf("venues = %+v", venues)
	}
}

type searchBody struct {
	SearchRequest struct {
		Filters map[string]any `json:"filters"`
		Cursor  string         `json:"cursor"`
	} `json:"search_request"`
}

func TestSearchRequest(t *testing.T) {
	c := newTestClient(t, "tok", "", "", func(w http.ResponseWriter, r *http.Request) {
		var body searchBody
		json.NewDecoder(r.Body).Decode(&body)

		f := body.SearchRequest.Filters
		if tags, _ := f["tag"].([]any); len(tags) != 1 || tags[0] != "1145" {
			t.Errorf("tag = %v", f["tag"])
		}

		if f["date"] != "2026-10-22" {
			t.Errorf("date = %v", f["date"])
		}

		bounds, _ := f["map_bounds"].(string)
		if !strings.HasPrefix(bounds, "139.") || strings.Count(bounds, ",") != 3 {
			t.Errorf("map_bounds = %q", bounds)
		}

		if body.SearchRequest.Cursor != "page2" {
			t.Errorf("cursor = %q", body.SearchRequest.Cursor)
		}

		io.WriteString(w, `{"nested_search":{"cursor":"page3","venues":[{"id":1,"name":"V","tz":"Asia/Tokyo","activities":null,
			"classes":[{"id":2,"name":"C","schedules":[{"id":3,"starttime":1792630800,"endtime":1792636800,
			"duration_minutes":100,"availability":{"credits":23,"status":"available"}}]}]}]}}`)
	})

	page, err := c.Search(context.Background(), SearchFilters{
		Lat: 35.64, Lon: 139.66, RadiusKm: 1, Date: "2026-10-22",
		Activity: &Activity{ID: "1145", Name: "Massage"},
	}, "page2")
	if err != nil {
		t.Fatal(err)
	}

	if page.Cursor != "page3" || len(page.Venues) != 1 || page.Venues[0].Classes[0].Schedules[0].Availability.Credits != 23 {
		t.Errorf("page = %+v", page)
	}
}

func TestSearchBrowseOmitsTag(t *testing.T) {
	c := newTestClient(t, "tok", "", "", func(w http.ResponseWriter, r *http.Request) {
		var body searchBody
		json.NewDecoder(r.Body).Decode(&body)

		if _, ok := body.SearchRequest.Filters["tag"]; ok {
			t.Error("browse search sent a tag")
		}

		if _, ok := body.SearchRequest.Filters["full_text_search"]; ok {
			t.Error("browse search sent full_text_search")
		}

		io.WriteString(w, `{"nested_search":{"venues":[]}}`)
	})

	if _, err := c.Search(context.Background(), SearchFilters{Lat: 35.64, Lon: 139.66, RadiusKm: 3, Date: "2026-10-22"}, ""); err != nil {
		t.Fatal(err)
	}
}

func TestVenueOfferingsDedupes(t *testing.T) {
	c := newTestClient(t, "tok", "", "", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"section_list":[
			{"class_list":[{"id":1,"name":"A","schedule_credits_from":34}]},
			{"class_list":[{"id":1,"name":"A"},{"id":2,"name":"B","schedule_credits_from":null}]}]}`)
	})

	got, err := c.VenueOfferings(context.Background(), 9)
	if err != nil {
		t.Fatal(err)
	}

	if len(got) != 2 || got[0].ID != 1 || *got[0].CreditsFrom != 34 || got[1].CreditsFrom != nil {
		t.Errorf("offerings = %+v", got)
	}
}

func TestVenueDetailFlattensSections(t *testing.T) {
	c := newTestClient(t, "tok", "", "", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"items":[
			{"key":"header","header":{"title":"B+TREE","tags":["Massage"," Facial"],"location":"Sangenjaya"}},
			{"key":"summary","summary":{"summary":"About text"}},
			{"key":"how_to_get_there","text_block":{"body":"Directions"}}]}`)
	})

	v, err := c.VenueDetail(context.Background(), 9)
	if err != nil {
		t.Fatal(err)
	}

	if v.Name != "B+TREE" || v.Subtitle != "Sangenjaya" || v.About != "About text" || v.HowToGetThere != "Directions" ||
		len(v.Tags) != 2 || v.Tags[1] != "Facial" {
		t.Errorf("venue = %+v", v)
	}
}

func TestReserveRejectsNonZeroState(t *testing.T) {
	c := newTestClient(t, "tok", "", "", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"state":3}`)
	})

	if _, err := c.Reserve(context.Background(), 42, 23); err == nil {
		t.Fatal("expected error")
	}
}

func TestConcurrentUnauthorizedLogsInOnce(t *testing.T) {
	var logins atomic.Int32
	c := newTestClient(t, "stale", "u@example.com", "pw", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v2/auth/login" {
			logins.Add(1)
			time.Sleep(20 * time.Millisecond)
			io.WriteString(w, `{"token":"fresh"}`)

			return
		}

		if r.Header.Get("Authorization") != "Token fresh" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		io.WriteString(w, `{"id":42}`)
	})

	var wg sync.WaitGroup
	for range 5 {
		wg.Go(func() {
			if _, err := c.Profile(context.Background()); err != nil {
				t.Error(err)
			}
		})
	}

	wg.Wait()

	if n := logins.Load(); n != 1 {
		t.Errorf("logins = %d, want 1", n)
	}
}

func TestResolvePlaceCached(t *testing.T) {
	var puts int
	c := newTestClient(t, "tok", "", "", func(w http.ResponseWriter, r *http.Request) {
		puts++
		io.WriteString(w, `{"data":{"location_history_item":{"lat":35.6,"lon":139.7}}}`)
	})

	for range 3 {
		if _, err := c.ResolvePlace(context.Background(), 5, "P"); err != nil {
			t.Fatal(err)
		}
	}

	if puts != 1 {
		t.Errorf("location history writes = %d, want 1", puts)
	}
}
