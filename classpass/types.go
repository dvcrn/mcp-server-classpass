package classpass

import "encoding/json"

type Profile struct {
	ID         int64  `json:"id"`
	FirstName  string `json:"first_name"`
	LastName   string `json:"last_name"`
	Email      string `json:"email"`
	Username   string `json:"username"`
	Timezone   string `json:"timezone"`
	Membership struct {
		PlanName    string `json:"plan_name"`
		PlanCredits int    `json:"plan_credits"`
		Status      string `json:"status"`
		CycleEnd    int64  `json:"cycle_end"`
	} `json:"membership"`
	ReservationInfo struct {
		CycleReservationCount    int `json:"cycle_reservation_count"`
		UpcomingReservationCount int `json:"upcoming_reservation_count"`
	} `json:"reservation_info"`
}

type Address struct {
	AddressLine1 string `json:"address_line1"`
	City         string `json:"city"`
}

type Booking struct {
	BookingID       string  `json:"booking_id"`
	VenueID         int64   `json:"venue_id"`
	VenueName       string  `json:"venue_name"`
	Status          string  `json:"status"`
	Timezone        string  `json:"timezone"`
	Credits         int     `json:"credits"`
	EntityType      string  `json:"entity_type"`
	Address         Address `json:"address"`
	ReservationData struct {
		ReservationID  int64 `json:"reservation_id"`
		RawReservation struct {
			ScheduleID int64  `json:"schedule_id"`
			ClassID    int64  `json:"class_id"`
			ClassName  string `json:"class_name"`
			StartTime  string `json:"start_time"`
			EndTime    string `json:"end_time"`
		} `json:"raw_reservation"`
	} `json:"reservation_data"`
}

type PlacePrediction struct {
	Description string `json:"description"`
	PlaceID     string `json:"place_id"`

	// Types are Google place types, e.g. "sublocality" or "transit_station" for areas and "establishment" for shops.
	Types []string `json:"types"`
}

type LatLon struct {
	Lat float64 `json:"lat"`
	Lon float64 `json:"lon"`
}

// Home is the city the user's ClassPass account is set to.
type Home struct {
	LatLon
	Name     string
	Timezone string
}

// VenueMatch is a venue found by name.
type VenueMatch struct {
	ID         int64   `json:"id"`
	Name       string  `json:"name"`
	Subtitle   string  `json:"subtitle"`
	DistanceKm float64 `json:"distance"`
}

type Place struct {
	Name             string  `json:"name"`
	FormattedAddress string  `json:"formatted_address"`
	Lat              float64 `json:"lat"`
	Lon              float64 `json:"lon"`
	Timezone         string  `json:"timezone"`
	RadiusKm         float64 `json:"calculated_radius_km"`
}

// Activity is a searchable category. Its ID is the search tag that actually filters results.
type Activity struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type SearchFilters struct {
	Lat      float64
	Lon      float64
	RadiusKm float64
	Date     string
	Activity *Activity
}

type SearchPage struct {
	Venues []Venue `json:"venues"`
	Cursor string  `json:"cursor"`
}

type Venue struct {
	ID         int64   `json:"id"`
	Name       string  `json:"name"`
	Subtitle   string  `json:"subtitle"`
	Activities *string `json:"activities"`
	DistanceKm float64 `json:"distance"`
	TZ         string  `json:"tz"`
	Address    Address `json:"address"`
	Ratings    struct {
		Mean  float64 `json:"mean"`
		Count struct {
			Total int `json:"total"`
		} `json:"count"`
	} `json:"ratings"`
	Classes []Class `json:"classes"`
}

type Class struct {
	ID         int64      `json:"id"`
	Name       string     `json:"name"`
	Activities string     `json:"activities"`
	Schedules  []Schedule `json:"schedules"`
}

type Schedule struct {
	ID              int64 `json:"id"`
	StartTime       int64 `json:"starttime"`
	EndTime         int64 `json:"endtime"`
	DurationMinutes int   `json:"duration_minutes"`
	Availability    struct {
		Credits int    `json:"credits"`
		Status  string `json:"status"`
	} `json:"availability"`
}

// VenueDetail is the venue page, flattened from its layout sections.
type VenueDetail struct {
	Name          string
	Subtitle      string
	Tags          []string
	About         string
	HowToGetThere string

	Rating float64

	// RatingsCount is ClassPass's display text, e.g. "2" or "1,000+".
	RatingsCount string
	Reviews      []Review
}

// Review is one of the few recent reviews shown on a venue page.
type Review struct {
	Class string `json:"title"`
	Text  string `json:"body"`
}

// Offering is a class a venue offers, independent of any time slot.
type Offering struct {
	ID              int64    `json:"id"`
	Name            string   `json:"name"`
	Activities      []string `json:"activities"`
	Level           string   `json:"level"`
	Description     string   `json:"description"`
	CreditsFrom     *int     `json:"schedule_credits_from"`
	DurationMinutes int      `json:"schedule_duration_minutes"`
}

type ScheduleListItem struct {
	Schedule
	Class struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	} `json:"class"`
	Venue struct {
		TZ string `json:"tz"`
	} `json:"venue"`
}

// ScheduleInfo is a single bookable slot with its class and venue.
type ScheduleInfo struct {
	ID               int64   `json:"id"`
	StartTime        float64 `json:"starttime"`
	EndTime          float64 `json:"endtime"`
	DurationMinutes  int     `json:"duration_minutes"`
	ReservationID    *int64  `json:"reservation_id"`
	LateCancellation string  `json:"late_cancellation"`
	Availability     struct {
		Credits int    `json:"credits"`
		Status  string `json:"status"`
	} `json:"availability"`
	Class struct {
		ID          int64  `json:"id"`
		Name        string `json:"name"`
		Activities  string `json:"activities"`
		Level       string `json:"level"`
		Description string `json:"description"`
		WhatToBring string `json:"what_to_bring"`
	} `json:"class"`
	Venue struct {
		ID       int64   `json:"id"`
		Name     string  `json:"name"`
		Subtitle string  `json:"subtitle"`
		TZ       string  `json:"tz"`
		Address  Address `json:"address"`
	} `json:"venue"`
}

type BookmarkedVenue struct {
	BookmarkID int64 `json:"bookmark_id"`

	ID       int64   `json:"id"`
	Name     string  `json:"name"`
	Subtitle string  `json:"subtitle"`
	Address  Address `json:"address"`
}

// ReservationModal is the checkout screen shown before reserving a schedule.
type ReservationModal struct {
	State int `json:"state"`
	Modal struct {
		Policy struct {
			Explainers []string `json:"explainers"`
		} `json:"policy"`
		Pricing struct {
			Credits int `json:"credits"`
		} `json:"pricing"`
		Footer struct {
			CTA []CTA `json:"cta"`
		} `json:"footer"`
	} `json:"modal"`
}

// CTA is an action button on a modal; Type is "credits" for reserve and "cancel" for cancel.
type CTA struct {
	Type    string `json:"type"`
	Label   string `json:"label"`
	Enabled bool   `json:"enabled"`
}

// CancelModal is the confirmation screen shown before cancelling a reservation.
// Message is null for a free cancellation; its shape when set is unknown.
type CancelModal struct {
	State         int   `json:"state"`
	ReservationID int64 `json:"reservation_id"`
	Modal         struct {
		Message json.RawMessage `json:"message"`
		Footer  struct {
			CTA []CTA `json:"cta"`
		} `json:"footer"`
	} `json:"modal"`
}

type Cancellation struct {
	Status        string `json:"status"`
	LateCancelled bool   `json:"late_cancelled"`
	Fee           string `json:"fee"`
	Title         string `json:"title"`
}

type Reservation struct {
	State          int   `json:"state"`
	ReservationID  int64 `json:"reservation_id"`
	CreditsBalance struct {
		CreditsTotal     int `json:"credits_total"`
		CreditsRemaining int `json:"credits_remaining"`
	} `json:"credits_balance"`
}

// Tag is a node in ClassPass's category tree, e.g. Food and Drink > Coffee. Searches filter by tag id.
type Tag struct {
	ID         string `json:"id"`
	ParentID   string `json:"parent_id"`
	Name       string `json:"name"`
	Filterable bool   `json:"filterable"`
	Subtags    []Tag  `json:"subtags"`
}

// VenueSearchFilters selects venues carrying TagID within RadiusKm of a point.
type VenueSearchFilters struct {
	Lat      float64
	Lon      float64
	RadiusKm float64
	Date     string
	TagID    string
	Count    int
}

type VenueSearchPage struct {
	TotalHits int          `json:"total_hits"`
	Venues    []FoundVenue `json:"venues"`
}

// FoundVenue is a venue from a tag search, without classes or slots.
type FoundVenue struct {
	ID           int64   `json:"id"`
	Name         string  `json:"name"`
	Subtitle     string  `json:"subtitle"`
	Activities   string  `json:"activities"`
	DistanceKm   float64 `json:"distance"`
	IsOpen       bool    `json:"is_open"`
	Rating       float64 `json:"rating_average"`
	RatingsCount string  `json:"rating_display_total"`
	Address      Address `json:"address"`
}
