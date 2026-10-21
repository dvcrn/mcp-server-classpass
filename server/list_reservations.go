package server

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type reservation struct {
	ReservationID int64  `json:"reservation_id"`
	SlotID        int64  `json:"slot_id"`
	ClassName     string `json:"class_name"`
	VenueID       int64  `json:"venue_id"`
	VenueName     string `json:"venue_name"`
	Address       string `json:"address"`
	Start         string `json:"start"`
	End           string `json:"end"`
	Credits       int    `json:"credits"`
	Status        string `json:"status"`
}

type reservationsInput struct {
	IncludePast bool `json:"include_past,omitempty" jsonschema:"also list past reservations (the 20 most recent overall, newest first); useful to rebook a previous visit"`
}

type reservationsOutput struct {
	Reservations []reservation `json:"reservations"`
}

func (h *handlers) listReservations(ctx context.Context, _ *mcp.CallToolRequest, in reservationsInput) (*mcp.CallToolResult, reservationsOutput, error) {
	list := h.cp.UpcomingBookings
	if in.IncludePast {
		list = h.cp.AllBookings
	}

	bookings, err := list(ctx)
	if err != nil {
		return nil, reservationsOutput{}, err
	}

	out := reservationsOutput{Reservations: []reservation{}}
	for _, b := range bookings {
		r := b.ReservationData.RawReservation

		out.Reservations = append(out.Reservations, reservation{
			ReservationID: b.ReservationData.ReservationID,
			SlotID:        r.ScheduleID,
			ClassName:     r.ClassName,
			VenueID:       b.VenueID,
			VenueName:     b.VenueName,
			Address:       joinAddress(b.Address),
			Start:         localTime(r.StartTime, b.Timezone),
			End:           localTime(r.EndTime, b.Timezone),
			Credits:       b.Credits,
			Status:        b.Status,
		})
	}

	return nil, out, nil
}
