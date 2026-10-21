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

	mu     sync.Mutex
	token  string
	userID int64
}

// New returns a client that uses token if set, otherwise logs in with username/password on first use.
func New(token, username, password string) *Client {
	jar, _ := cookiejar.New(nil)

	return &Client{
		BaseURL:   DefaultBaseURL,
		http:      &http.Client{Jar: jar},
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
	if c.currentToken() == "" {
		if err := c.Login(ctx); err != nil {
			return err
		}
	}

	err := c.send(ctx, method, path, c.currentToken(), body, out)
	if errors.Is(err, errUnauthorized) && c.canLogin() {
		if err := c.Login(ctx); err != nil {
			return err
		}

		err = c.send(ctx, method, path, c.currentToken(), body, out)
	}

	return err
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

func (c *Client) UpcomingBookings(ctx context.Context) ([]Booking, error) {
	var out struct {
		Bookings []Booking `json:"bookings"`
	}

	if err := c.do(ctx, http.MethodGet, "/bff/v1/users/me/bookings?upcoming_only=true", nil, &out); err != nil {
		return nil, err
	}

	return out.Bookings, nil
}

func (c *Client) LocationAutocomplete(ctx context.Context, query string) ([]PlacePrediction, error) {
	var out struct {
		Data struct {
			Predictions []PlacePrediction `json:"predictions"`
		} `json:"data"`
	}

	body := map[string]string{"autocomplete_type": "geocode", "query": query}
	if err := c.do(ctx, http.MethodPost, "/unisearch/v1/location/autocomplete", body, &out); err != nil {
		return nil, err
	}

	return out.Data.Predictions, nil
}

// ResolvePlace geocodes a place id. ClassPass only exposes this by adding the place to the user's location history.
func (c *Client) ResolvePlace(ctx context.Context, userID int64, placeID string) (*Place, error) {
	var out struct {
		Data struct {
			Item Place `json:"location_history_item"`
		} `json:"data"`
	}

	path := fmt.Sprintf("/unisearch/v1/users/%d/locations/%s", userID, url.PathEscape(placeID))
	if err := c.do(ctx, http.MethodPut, path, nil, &out); err != nil {
		return nil, err
	}

	return &out.Data.Item, nil
}

// Activities returns the activity categories matching query near the given point.
func (c *Client) Activities(ctx context.Context, query string, lat, lon float64) ([]Activity, error) {
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
		return nil, err
	}

	// Entries of other result types (venues, classes) use numeric ids, so only activities are decoded.
	for _, r := range out.Data.Results {
		if r.Type != "activities" {
			continue
		}

		var acts []Activity
		if err := json.Unmarshal(r.Entries, &acts); err != nil {
			return nil, fmt.Errorf("classpass: decode activities: %w", err)
		}

		return acts, nil
	}

	return nil, nil
}

// Search returns venues offering the activity within RadiusKm of the point, with their bookable schedules on Date.
func (c *Client) Search(ctx context.Context, f SearchFilters) ([]Venue, error) {
	dLat := f.RadiusKm / 111.0
	dLon := f.RadiusKm / (111.0 * math.Cos(f.Lat*math.Pi/180))

	filters := map[string]any{
		"find_next_available":  true,
		"exclude_past_booking": true,
		"lat":                  f.Lat,
		"lon":                  f.Lon,
		"date":                 f.Date,
		"map_bounds":           fmt.Sprintf("%f,%f,%f,%f", f.Lon-dLon, f.Lat-dLat, f.Lon+dLon, f.Lat+dLat),
		"tag":                  []string{f.Activity.ID},
		"full_text_search":     map[string]string{"tag_names": f.Activity.Name},
	}

	var out struct {
		NestedSearch struct {
			Venues []Venue `json:"venues"`
		} `json:"nested_search"`
	}

	body := map[string]any{"search_request": map[string]any{"filters": filters}}
	if err := c.do(ctx, http.MethodPost, "/unisearch/v3/layout/search", body, &out); err != nil {
		return nil, err
	}

	return out.NestedSearch.Venues, nil
}
