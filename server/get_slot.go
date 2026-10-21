package server

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type getSlotInput struct {
	SlotID int64 `json:"slot_id" jsonschema:"id of a bookable time slot, from search_classes, list_slots, or list_reservations"`
}

type slotDetail struct {
	SlotID             int64  `json:"slot_id"`
	ClassID            int64  `json:"class_id"`
	ClassName          string `json:"class_name"`
	Activities         string `json:"activities"`
	Level              string `json:"level"`
	Description        string `json:"description"`
	WhatToBring        string `json:"what_to_bring"`
	VenueID            int64  `json:"venue_id"`
	VenueName          string `json:"venue_name"`
	Address            string `json:"address"`
	Start              string `json:"start"`
	End                string `json:"end"`
	DurationMinutes    int    `json:"duration_minutes"`
	Credits            int    `json:"credits"`
	Status             string `json:"status"`
	ReservationID      *int64 `json:"reservation_id,omitempty"`
	CancellationPolicy string `json:"cancellation_policy"`
}

func (h *handlers) getSlot(ctx context.Context, _ *mcp.CallToolRequest, in getSlotInput) (*mcp.CallToolResult, slotDetail, error) {
	s, err := h.cp.Schedule(ctx, in.SlotID)
	if err != nil {
		return nil, slotDetail{}, err
	}

	policy, err := h.cp.CancellationPolicy(ctx, in.SlotID)
	if err != nil {
		return nil, slotDetail{}, err
	}

	return nil, slotDetail{
		SlotID:             s.ID,
		ClassID:            s.Class.ID,
		ClassName:          s.Class.Name,
		Activities:         s.Class.Activities,
		Level:              s.Class.Level,
		Description:        s.Class.Description,
		WhatToBring:        s.Class.WhatToBring,
		VenueID:            s.Venue.ID,
		VenueName:          venueName(s.Venue.Name, s.Venue.Subtitle),
		Address:            joinAddress(s.Venue.Address),
		Start:              unixLocal(int64(s.StartTime), s.Venue.TZ),
		End:                unixLocal(int64(s.EndTime), s.Venue.TZ),
		DurationMinutes:    s.DurationMinutes,
		Credits:            s.Availability.Credits,
		Status:             s.Availability.Status,
		ReservationID:      s.ReservationID,
		CancellationPolicy: policy,
	}, nil
}
