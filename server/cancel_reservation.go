package server

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type cancelInput struct {
	ReservationID int64 `json:"reservation_id" jsonschema:"reservation id from list_reservations"`
	ConfirmLate   bool  `json:"confirm_late,omitempty" jsonschema:"set only after the user accepted the warning a previous call returned, e.g. forfeiting credits for a late cancellation"`
}

type cancelOutput struct {
	Status        string `json:"status"`
	LateCancelled bool   `json:"late_cancelled"`
	Fee           string `json:"fee"`
	Title         string `json:"title"`
}

func (h *handlers) cancelReservation(ctx context.Context, _ *mcp.CallToolRequest, in cancelInput) (*mcp.CallToolResult, cancelOutput, error) {
	bookings, err := h.cp.UpcomingBookings(ctx)
	if err != nil {
		return nil, cancelOutput{}, err
	}

	// The confirmation modal is keyed by schedule; looking it up here also limits cancels to the user's upcoming reservations.
	var scheduleID int64
	for _, b := range bookings {
		if b.ReservationData.ReservationID == in.ReservationID {
			scheduleID = b.ReservationData.RawReservation.ScheduleID
			break
		}
	}

	if scheduleID == 0 {
		return nil, cancelOutput{}, fmt.Errorf("reservation %d is not among the upcoming reservations", in.ReservationID)
	}

	modal, err := h.cp.CancelModal(ctx, scheduleID)
	if err != nil {
		return nil, cancelOutput{}, err
	}

	if modal.State != 0 || modal.ReservationID != in.ReservationID || !hasEnabledCTA(modal.Modal.Footer.CTA, "cancel") {
		return nil, cancelOutput{}, fmt.Errorf("reservation %d cannot be cancelled", in.ReservationID)
	}

	if warning := modalMessage(modal.Modal.Message); warning != "" && !in.ConfirmLate {
		return nil, cancelOutput{}, fmt.Errorf("not cancelled; ClassPass warns: %s. Ask the user, then call again with confirm_late=true to proceed", warning)
	}

	c, err := h.cp.CancelReservation(ctx, in.ReservationID)
	if err != nil {
		return nil, cancelOutput{}, err
	}

	return nil, cancelOutput{Status: c.Status, LateCancelled: c.LateCancelled, Fee: c.Fee, Title: c.Title}, nil
}

// modalMessage renders a modal message as text: strings as-is, other JSON compacted, null as empty.
func modalMessage(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}

	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}

	return string(raw)
}
