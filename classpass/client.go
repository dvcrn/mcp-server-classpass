package classpass

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	DefaultBaseURL = "https://api.classpass.com"
	appVersion     = "10.27.0"
	userAgent      = "ClassPass/10.27.0 (com.classpass.classpass; build:5491; iOS 27.2.0) Alamofire/5.10.2"

	// iOS app's OAuth client id, sent with every login.
	loginClientID = "82ec5ad5-ad9d-11e4-bf55-22000b44012e"
)

var errUnauthorized = errors.New("classpass: unauthorized")

// Client talks to the ClassPass mobile API, impersonating the iOS app.
// Cloudflare blocks curl's HTTP/2 fingerprint but accepts Go's default transport.
type Client struct {
	BaseURL   string
	http      *http.Client
	username  string
	password  string
	sessionID string

	// loginMu serializes logins so concurrent 401s refresh the token once.
	loginMu sync.Mutex

	mu     sync.Mutex
	token  string
	userID int64
	home   *Home
	places map[string]*Place
	tags   []Tag
}

// New returns a client that uses token if set, otherwise logs in with username/password on first use.
func New(token, username, password string) *Client {
	jar, _ := cookiejar.New(nil)

	return &Client{
		BaseURL:   DefaultBaseURL,
		http:      &http.Client{Jar: jar, Timeout: 30 * time.Second},
		username:  username,
		password:  password,
		sessionID: strings.ToUpper(rand.Text()),
		token:     token,
	}
}

func (c *Client) canLogin() bool { return c.username != "" && c.password != "" }

func (c *Client) Login(ctx context.Context) error {
	if !c.canLogin() {
		return errors.New("classpass: no token and no username/password configured")
	}

	var out struct {
		Token string `json:"token"`
	}

	body := map[string]string{"client_id": loginClientID, "username": c.username, "password": c.password}
	if err := c.send(ctx, http.MethodPost, "/v2/auth/login", "", body, &out); err != nil {
		return fmt.Errorf("classpass: login: %w", err)
	}

	if out.Token == "" {
		return errors.New("classpass: login response has no token")
	}

	c.mu.Lock()
	c.token = out.Token
	c.mu.Unlock()

	return nil
}

func (c *Client) currentToken() string {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.token
}

// do sends an authenticated request, logging in first if needed and once more on a 401.
func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	token := c.currentToken()
	if token == "" {
		if err := c.relogin(ctx, ""); err != nil {
			return err
		}

		token = c.currentToken()
	}

	err := c.send(ctx, method, path, token, body, out)
	if errors.Is(err, errUnauthorized) && c.canLogin() {
		if err := c.relogin(ctx, token); err != nil {
			return err
		}

		err = c.send(ctx, method, path, c.currentToken(), body, out)
	}

	return err
}

// relogin logs in unless another request already replaced the stale token while this one waited.
func (c *Client) relogin(ctx context.Context, stale string) error {
	c.loginMu.Lock()
	defer c.loginMu.Unlock()

	if c.currentToken() != stale {
		return nil
	}

	return c.Login(ctx)
}

func (c *Client) send(ctx context.Context, method, path, token string, body, out any) error {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}

		r = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, r)
	if err != nil {
		return err
	}

	h := req.Header
	h.Set("Accept", "*/*")
	h.Set("App-Version", appVersion)
	h.Set("Accept-Language", "en")
	h.Set("platform", "ios")
	h.Set("Cache-Control", "no-store")
	h.Set("X-CP-Session-ID", c.sessionID)
	h.Set("X-CP-Device", "iPad")
	h.Set("User-Agent", userAgent)

	if body != nil {
		h.Set("Content-Type", "application/json")
	}

	if token != "" {
		h.Set("Authorization", "Token "+token)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	if resp.StatusCode == http.StatusUnauthorized {
		return errUnauthorized
	}

	if resp.StatusCode >= 300 {
		return fmt.Errorf("classpass: %s %s: %s: %.200s", method, path, resp.Status, data)
	}

	if out == nil {
		return nil
	}

	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("classpass: %s %s: decode: %w", method, path, err)
	}

	return nil
}

func (c *Client) Profile(ctx context.Context) (*Profile, error) {
	var p Profile
	if err := c.do(ctx, http.MethodGet, "/v1/me/profile", nil, &p); err != nil {
		return nil, err
	}

	c.mu.Lock()
	c.userID = p.ID
	c.mu.Unlock()

	return &p, nil
}

// UserID returns the logged-in user's id, fetching the profile on first use.
func (c *Client) UserID(ctx context.Context) (int64, error) {
	c.mu.Lock()
	id := c.userID
	c.mu.Unlock()

	if id != 0 {
		return id, nil
	}

	p, err := c.Profile(ctx)
	if err != nil {
		return 0, err
	}

	return p.ID, nil
}

func (c *Client) CreditsRemaining(ctx context.Context, userID int64) (int, error) {
	var out struct {
		Data struct {
			CreditBalance struct {
				CreditsRemaining int `json:"credits_remaining"`
			} `json:"credit_balance"`
		} `json:"data"`
	}

	if err := c.do(ctx, http.MethodGet, fmt.Sprintf("/v3/lifecycle/user/%d/balance", userID), nil, &out); err != nil {
		return 0, err
	}

	return out.Data.CreditBalance.CreditsRemaining, nil
}

// UpcomingBookings returns the user's upcoming reservations, soonest first.
func (c *Client) UpcomingBookings(ctx context.Context) ([]Booking, error) {
	return c.bookings(ctx, true)
}

// AllBookings returns the user's most recent 20 reservations, past and upcoming, newest first.
func (c *Client) AllBookings(ctx context.Context) ([]Booking, error) {
	return c.bookings(ctx, false)
}

func (c *Client) bookings(ctx context.Context, upcomingOnly bool) ([]Booking, error) {
	var out struct {
		Bookings []Booking `json:"bookings"`
	}

	path := fmt.Sprintf("/bff/v1/users/me/bookings?upcoming_only=%t", upcomingOnly)
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}

	return out.Bookings, nil
}

// LocationAutocomplete returns place matches for query, ranked by Google with a bias toward near when given.
// Shops and venues named after a city often outrank the city itself.
func (c *Client) LocationAutocomplete(ctx context.Context, query string, near *LatLon) ([]PlacePrediction, error) {
	var out struct {
		Data struct {
			Predictions []PlacePrediction `json:"predictions"`
		} `json:"data"`
	}

	body := map[string]any{"autocomplete_type": "geocode", "query": query}
	if near != nil {
		body["near"] = near
	}

	if err := c.do(ctx, http.MethodPost, "/unisearch/v1/location/autocomplete", body, &out); err != nil {
		return nil, err
	}

	return out.Data.Predictions, nil
}

// ResolvePlace geocodes a place id. ClassPass only exposes this by adding the place to the user's location history,
// so results are cached to write each place at most once per client.
func (c *Client) ResolvePlace(ctx context.Context, userID int64, placeID string) (*Place, error) {
	c.mu.Lock()
	cached := c.places[placeID]
	c.mu.Unlock()

	if cached != nil {
		return cached, nil
	}

	var out struct {
		Data struct {
			Item Place `json:"location_history_item"`
		} `json:"data"`
	}

	path := fmt.Sprintf("/unisearch/v1/users/%d/locations/%s", userID, url.PathEscape(placeID))
	if err := c.do(ctx, http.MethodPut, path, nil, &out); err != nil {
		return nil, err
	}

	c.mu.Lock()
	if c.places == nil {
		c.places = map[string]*Place{}
	}

	c.places[placeID] = &out.Data.Item
	c.mu.Unlock()

	return &out.Data.Item, nil
}

// Keywords returns the activity categories and venues matching query near the given point, best first.
func (c *Client) Keywords(ctx context.Context, query string, lat, lon float64) ([]Activity, []VenueMatch, error) {
	var out struct {
		Data struct {
			Results []struct {
				Type    string          `json:"type"`
				Entries json.RawMessage `json:"entries"`
			} `json:"results"`
		} `json:"data"`
	}

	body := map[string]any{
		"query":            query,
		"location":         map[string]float64{"lat": lat, "lon": lon},
		"full_text_search": map[string]any{},
	}
	if err := c.do(ctx, http.MethodPost, "/unisearch/v1/keyword/autocomplete", body, &out); err != nil {
		return nil, nil, err
	}

	// Result types disagree on the id type (activity ids are strings, venue ids numbers), so each is decoded separately.
	var acts []Activity
	var venues []VenueMatch
	for _, r := range out.Data.Results {
		var err error
		switch r.Type {
		case "activities":
			err = json.Unmarshal(r.Entries, &acts)

		case "venues":
			err = json.Unmarshal(r.Entries, &venues)
		}

		if err != nil {
			return nil, nil, fmt.Errorf("classpass: decode %s: %w", r.Type, err)
		}
	}

	return acts, venues, nil
}

// Search returns one page of venues within RadiusKm of the point, with their bookable schedules on Date.
// Without an Activity it returns every venue in range. Pass the returned page's Cursor to fetch the next page.
func (c *Client) Search(ctx context.Context, f SearchFilters, cursor string) (*SearchPage, error) {
	dLat := f.RadiusKm / 111.0
	dLon := f.RadiusKm / (111.0 * math.Cos(f.Lat*math.Pi/180))

	filters := map[string]any{
		"find_next_available":  true,
		"exclude_past_booking": true,
		"lat":                  f.Lat,
		"lon":                  f.Lon,
		"date":                 f.Date,
		"map_bounds":           fmt.Sprintf("%f,%f,%f,%f", f.Lon-dLon, f.Lat-dLat, f.Lon+dLon, f.Lat+dLat),
	}

	// The tag is what filters; full_text_search alone is ignored by the API.
	if f.Activity != nil {
		filters["tag"] = []string{f.Activity.ID}
		filters["full_text_search"] = map[string]string{"tag_names": f.Activity.Name}
	}

	req := map[string]any{"filters": filters}
	if cursor != "" {
		req["cursor"] = cursor
	}

	var out struct {
		NestedSearch SearchPage `json:"nested_search"`
	}

	body := map[string]any{"search_request": req}
	if err := c.do(ctx, http.MethodPost, "/unisearch/v3/layout/search", body, &out); err != nil {
		return nil, err
	}

	return &out.NestedSearch, nil
}

func (c *Client) VenueDetail(ctx context.Context, venueID int64) (*VenueDetail, error) {
	var out struct {
		Items []struct {
			Key    string `json:"key"`
			Header *struct {
				Title    string   `json:"title"`
				Tags     []string `json:"tags"`
				Location string   `json:"location"`
			} `json:"header"`
			Summary *struct {
				Summary string `json:"summary"`
			} `json:"summary"`
			TextBlock *struct {
				Body string `json:"body"`
			} `json:"text_block"`
			Reviews *struct {
				Rating          float64  `json:"rating"`
				NumberOfRatings string   `json:"number_of_ratings"`
				Reviews         []Review `json:"reviews"`
			} `json:"reviews"`
		} `json:"items"`
	}

	if err := c.do(ctx, http.MethodGet, fmt.Sprintf("/bff/v2/venue_detail/%d", venueID), nil, &out); err != nil {
		return nil, err
	}

	var v VenueDetail
	for _, it := range out.Items {
		switch {
		case it.Header != nil:
			v.Name, v.Subtitle = it.Header.Title, it.Header.Location
			for _, t := range it.Header.Tags {
				v.Tags = append(v.Tags, strings.TrimSpace(t))
			}

		case it.Summary != nil:
			v.About = it.Summary.Summary

		case it.Key == "how_to_get_there" && it.TextBlock != nil:
			v.HowToGetThere = it.TextBlock.Body

		case it.Reviews != nil:
			v.Rating, v.RatingsCount, v.Reviews = it.Reviews.Rating, it.Reviews.NumberOfRatings, it.Reviews.Reviews
		}
	}

	return &v, nil
}

// VenueOfferings returns the classes a venue offers. A class listed under several sections appears once.
func (c *Client) VenueOfferings(ctx context.Context, venueID int64) ([]Offering, error) {
	var out struct {
		SectionList []struct {
			ClassList []Offering `json:"class_list"`
		} `json:"section_list"`
	}

	if err := c.do(ctx, http.MethodGet, fmt.Sprintf("/bff/v1/venue_offerings/%d", venueID), nil, &out); err != nil {
		return nil, err
	}

	seen := map[int64]bool{}
	var offerings []Offering
	for _, s := range out.SectionList {
		for _, o := range s.ClassList {
			if seen[o.ID] {
				continue
			}

			seen[o.ID] = true
			offerings = append(offerings, o)
		}
	}

	return offerings, nil
}

// ClassSchedules returns upcoming slots of one class at a venue, starting on date and spanning several days.
func (c *Client) ClassSchedules(ctx context.Context, venueID, classID int64, date string) ([]ScheduleListItem, error) {
	var out struct {
		ScheduleList []ScheduleListItem `json:"schedule_list"`
	}

	q := url.Values{"class_id": {fmt.Sprint(classID)}, "date": {date}}
	path := fmt.Sprintf("/bff/v1/venue_schedule_list/%d?%s", venueID, q.Encode())
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}

	return out.ScheduleList, nil
}

func (c *Client) Schedule(ctx context.Context, scheduleID int64) (*ScheduleInfo, error) {
	var s ScheduleInfo
	if err := c.do(ctx, http.MethodGet, fmt.Sprintf("/v1/schedules/%d", scheduleID), nil, &s); err != nil {
		return nil, err
	}

	return &s, nil
}

// CancellationPolicy returns the human-readable cancellation policy shown on the schedule page.
func (c *Client) CancellationPolicy(ctx context.Context, scheduleID int64) (string, error) {
	var out struct {
		Items []struct {
			Key       string `json:"key"`
			TextBlock *struct {
				Body string `json:"body"`
			} `json:"text_block"`
		} `json:"items"`
	}

	if err := c.do(ctx, http.MethodGet, fmt.Sprintf("/bff/v3/schedule_detail/%d", scheduleID), nil, &out); err != nil {
		return "", err
	}

	for _, it := range out.Items {
		if it.Key == "cancellation_policy" && it.TextBlock != nil {
			return it.TextBlock.Body, nil
		}
	}

	return "", nil
}

func (c *Client) Bookmarks(ctx context.Context) ([]BookmarkedVenue, error) {
	var out struct {
		Bookmarks []struct {
			Type  string          `json:"type"`
			Venue BookmarkedVenue `json:"venue"`
		} `json:"bookmarks"`
	}

	if err := c.do(ctx, http.MethodGet, "/v1/me/bookmarks", nil, &out); err != nil {
		return nil, err
	}

	var venues []BookmarkedVenue
	for _, b := range out.Bookmarks {
		if b.Type == "venue" {
			venues = append(venues, b.Venue)
		}
	}

	return venues, nil
}

func (c *Client) ReservationModal(ctx context.Context, scheduleID int64) (*ReservationModal, error) {
	var m ReservationModal
	if err := c.do(ctx, http.MethodGet, fmt.Sprintf("/bff/v2/reservation_modal/%d", scheduleID), nil, &m); err != nil {
		return nil, err
	}

	return &m, nil
}

// Reserve books a schedule with credits. credits must match the price from ReservationModal.
func (c *Client) Reserve(ctx context.Context, scheduleID int64, credits int) (*Reservation, error) {
	body := map[string]any{
		"opted_in":                false,
		"event_metadata":          map[string]any{},
		"credits":                 credits,
		"type":                    "credits",
		"flexible_booking_window": false,
	}

	var r Reservation
	if err := c.do(ctx, http.MethodPost, fmt.Sprintf("/bff/v1/reservation_modal/%d/reserve", scheduleID), body, &r); err != nil {
		return nil, err
	}

	if r.State != 0 || r.ReservationID == 0 {
		return nil, fmt.Errorf("classpass: reserve schedule %d: unexpected state %d", scheduleID, r.State)
	}

	return &r, nil
}

func (c *Client) CancelModal(ctx context.Context, scheduleID int64) (*CancelModal, error) {
	var m CancelModal
	if err := c.do(ctx, http.MethodGet, fmt.Sprintf("/bff/v1/reservation_modal/%d/cancel", scheduleID), nil, &m); err != nil {
		return nil, err
	}

	return &m, nil
}

// CancelReservation cancels a reservation. Late cancellations can forfeit credits or charge a fee.
func (c *Client) CancelReservation(ctx context.Context, reservationID int64) (*Cancellation, error) {
	var out Cancellation
	body := map[string]any{"reason": nil}
	if err := c.do(ctx, http.MethodPost, fmt.Sprintf("/v1/reservations/%d/cancel", reservationID), body, &out); err != nil {
		return nil, err
	}

	if out.Status != "CANCELLED" {
		return nil, fmt.Errorf("classpass: cancel reservation %d: unexpected status %q", reservationID, out.Status)
	}

	return &out, nil
}

// HomeLocation returns the city the user's ClassPass account is set to, fetched once per client.
func (c *Client) HomeLocation(ctx context.Context) (*Home, error) {
	c.mu.Lock()
	home := c.home
	c.mu.Unlock()

	if home != nil {
		return home, nil
	}

	uid, err := c.UserID(ctx)
	if err != nil {
		return nil, err
	}

	var out struct {
		Location struct {
			Name     string `json:"name"`
			Timezone string `json:"timezone"`
			Centroid struct {
				Latitude  float64 `json:"latitude"`
				Longitude float64 `json:"longitude"`
			} `json:"centroid"`
		} `json:"location"`
	}

	if err := c.do(ctx, http.MethodGet, fmt.Sprintf("/v1/gls/user/%d/location", uid), nil, &out); err != nil {
		return nil, err
	}

	l := out.Location
	home = &Home{
		Name:     l.Name,
		Timezone: l.Timezone,
		LatLon:   LatLon{Lat: l.Centroid.Latitude, Lon: l.Centroid.Longitude},
	}

	c.mu.Lock()
	c.home = home
	c.mu.Unlock()

	return home, nil
}

// AddBookmark saves a venue for the user and returns the new bookmark's id.
func (c *Client) AddBookmark(ctx context.Context, venueID int64) (int64, error) {
	var out struct {
		ID int64 `json:"id"`
	}

	body := map[string]any{"ref_id": venueID, "type": "venue"}
	if err := c.do(ctx, http.MethodPost, "/v1/me/bookmarks", body, &out); err != nil {
		return 0, err
	}

	return out.ID, nil
}

// RemoveBookmark deletes a bookmark by its bookmark id, not the venue id.
func (c *Client) RemoveBookmark(ctx context.Context, bookmarkID int64) error {
	return c.do(ctx, http.MethodDelete, fmt.Sprintf("/v1/me/bookmarks/%d", bookmarkID), nil, nil)
}

// Tags returns ClassPass's category tree, fetched once per client.
func (c *Client) Tags(ctx context.Context) ([]Tag, error) {
	c.mu.Lock()
	tags := c.tags
	c.mu.Unlock()

	if tags != nil {
		return tags, nil
	}

	var out struct {
		Tags []Tag `json:"tags"`
	}

	if err := c.do(ctx, http.MethodGet, "/bff/v1/tags", nil, &out); err != nil {
		return nil, err
	}

	c.mu.Lock()
	c.tags = out.Tags
	c.mu.Unlock()

	return out.Tags, nil
}

// SearchVenues returns up to Count venues tagged TagID near the point. Unlike Search it carries no classes or
// slots, so one request covers far more venues.
func (c *Client) SearchVenues(ctx context.Context, f VenueSearchFilters) (*VenueSearchPage, error) {
	dLat := f.RadiusKm / 111.0
	dLon := f.RadiusKm / (111.0 * math.Cos(f.Lat*math.Pi/180))

	body := map[string]any{
		"sort_by_is_open":         true,
		"exclude_past_booking":    true,
		"find_next_available":     true,
		"lat":                     f.Lat,
		"lon":                     f.Lon,
		"viewport_south_west_lat": f.Lat - dLat,
		"viewport_south_west_lon": f.Lon - dLon,
		"viewport_north_east_lat": f.Lat + dLat,
		"viewport_north_east_lon": f.Lon + dLon,
		"date":                    f.Date,
		"tag":                     []string{f.TagID},
		"count":                   f.Count,
	}

	var page VenueSearchPage
	if err := c.do(ctx, http.MethodPost, "/unisearch/v1/venues", body, &page); err != nil {
		return nil, err
	}

	return &page, nil
}
