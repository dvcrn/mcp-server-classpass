package classpass

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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

func TestActivitiesSkipsNumericIDEntries(t *testing.T) {
	c := newTestClient(t, "tok", "", "", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"status":200,"data":{"results":[
			{"type":"activities","entries":[{"type":"activity","id":"1145","name":"Massage"}]},
			{"type":"venues","entries":[{"type":"venue","id":282110,"name":"B+TREE"}]}]}}`)
	})
	acts, err := c.Activities(context.Background(), "massage", 35.6, 139.6)
	if err != nil {
		t.Fatal(err)
	}
	if len(acts) != 1 || acts[0] != (Activity{ID: "1145", Name: "Massage"}) {
		t.Errorf("activities = %+v", acts)
	}
}

func TestSearchRequest(t *testing.T) {
	c := newTestClient(t, "tok", "", "", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			SearchRequest struct {
				Filters map[string]any `json:"filters"`
			} `json:"search_request"`
		}
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
		io.WriteString(w, `{"nested_search":{"venues":[{"id":1,"name":"V","tz":"Asia/Tokyo","activities":null,
			"classes":[{"id":2,"name":"C","schedules":[{"id":3,"starttime":1792630800,"endtime":1792636800,
			"duration_minutes":100,"availability":{"credits":23,"status":"available"}}]}]}]}}`)
	})
	venues, err := c.Search(context.Background(), SearchFilters{
		Lat: 35.64, Lon: 139.66, RadiusKm: 1, Date: "2026-10-22",
		Activity: Activity{ID: "1145", Name: "Massage"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(venues) != 1 || venues[0].Classes[0].Schedules[0].Availability.Credits != 23 {
		t.Errorf("venues = %+v", venues)
	}
}
