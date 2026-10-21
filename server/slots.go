package server

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	// maxVenueClasses caps how many classes list_slots fetches when no class_id is given; each class is a separate request.
	maxVenueClasses = 8

	// maxSlotPages caps requests per class; each page holds up to 50 slots.
	maxSlotPages = 3
)

type listSlotsInput struct {
	VenueID    int64  `json:"venue_id" jsonschema:"venue id"`
	ClassID    int64  `json:"class_id,omitempty" jsonschema:"class id from get_venue or search_classes; omit to list every class at the venue (venues with at most 8 classes)"`
	Date       string `json:"date,omitempty" jsonschema:"first day in YYYY-MM-DD; defaults to today"`
	EndDate    string `json:"end_date,omitempty" jsonschema:"last day in YYYY-MM-DD, at most 3 days after date; defaults to date+3 for one class and to date when listing every class"`
	TimeAfter  string `json:"time_after,omitempty" jsonschema:"earliest slot start, venue-local HH:MM, e.g. 07:30"`
	TimeBefore string `json:"time_before,omitempty" jsonschema:"latest slot start, venue-local HH:MM, e.g. 18:00"`
	MaxCredits *int   `json:"max_credits,omitempty" jsonschema:"only slots costing at most this many credits"`
}

type listSlotsOutput struct {
	Date    string  `json:"date"`
	EndDate string  `json:"end_date"`
	Classes []class `json:"classes"`
	Note    string  `json:"note,omitempty"`
}

func (h *handlers) listSlots(ctx context.Context, _ *mcp.CallToolRequest, in listSlotsInput) (*mcp.CallToolResult, listSlotsOutput, error) {
	date, err := parseDate(ctx, h.cp, in.Date)
	if err != nil {
		return nil, listSlotsOutput{}, err
	}

	end := in.EndDate
	if end == "" && in.ClassID != 0 {
		start, _ := time.Parse(time.DateOnly, date)
		end = start.AddDate(0, 0, maxDays-1).Format(time.DateOnly)
	}

	days, err := dateRange(date, end)
	if err != nil {
		return nil, listSlotsOutput{}, err
	}

	sf, err := newSlotFilter(in.MaxCredits, in.TimeAfter, in.TimeBefore, 0)
	if err != nil {
		return nil, listSlotsOutput{}, err
	}

	classIDs := []int64{in.ClassID}
	if in.ClassID == 0 {
		offerings, err := h.cp.VenueOfferings(ctx, in.VenueID)
		if err != nil {
			return nil, listSlotsOutput{}, err
		}

		if len(offerings) > maxVenueClasses {
			return nil, listSlotsOutput{}, fmt.Errorf("venue %d offers %d classes; pass a class_id from get_venue", in.VenueID, len(offerings))
		}

		classIDs = classIDs[:0]
		for _, o := range offerings {
			classIDs = append(classIDs, o.ID)
		}
	}

	out := listSlotsOutput{Date: days[0], EndDate: days[len(days)-1], Classes: []class{}}
	for _, id := range classIDs {
		c, err := h.classSlots(ctx, in.VenueID, id, days[0], days[len(days)-1], sf)
		if err != nil {
			return nil, listSlotsOutput{}, err
		}

		if len(c.Slots) > 0 {
			out.Classes = append(out.Classes, c)
		}
	}

	if len(out.Classes) == 0 {
		out.Note = "No bookable slots in this range. Try later dates or looser filters."
	}

	return nil, out, nil
}

// classSlots collects one class's slots from start to end. Each upstream page starts at a date and holds up to
// 50 slots, so the next page starts on the last day seen and repeats are skipped.
func (h *handlers) classSlots(ctx context.Context, venueID, classID int64, start, end string, sf slotFilter) (class, error) {
	c := class{ClassID: classID, Slots: []slot{}}
	seen := map[int64]bool{}
	date := start

	for range maxSlotPages {
		items, err := h.cp.ClassSchedules(ctx, venueID, classID, date)
		if err != nil {
			return class{}, err
		}

		added, past := 0, false
		for _, it := range items {
			if seen[it.ID] {
				continue
			}

			seen[it.ID] = true
			added++

			loc := venueLocation(it.Venue.TZ)
			day := time.Unix(it.StartTime, 0).In(loc).Format(time.DateOnly)
			if day > end {
				past = true
				continue
			}

			date = max(date, day)
			c.Name = cmp.Or(c.Name, it.Class.Name)
			if day >= start && sf.keep(it.Schedule, loc) {
				c.Slots = append(c.Slots, toSlot(it.Schedule, it.Venue.TZ))
			}
		}

		if past || added == 0 {
			break
		}
	}

	slices.SortFunc(c.Slots, func(a, b slot) int { return cmp.Compare(a.Start, b.Start) })

	return c, nil
}

// venueLocation loads a venue's timezone, falling back to UTC.
func venueLocation(tz string) *time.Location {
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return time.UTC
	}

	return loc
}
