package server

import (
	"context"
	"fmt"

	"github.com/dvcrn/mcp-server-classpass/classpass"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type bookInput struct {
	SlotID int64 `json:"slot_id" jsonschema:"id of the time slot to book"`
}

type bookOutput struct {
	ReservationID      int64  `json:"reservation_id"`
	CreditsCharged     int    `json:"credits_charged"`
	CreditsRemaining   int    `json:"credits_remaining"`
	CancellationPolicy string `json:"cancellation_policy"`
}

func (h *handlers) bookSlot(ctx context.Context, _ *mcp.CallToolRequest, in bookInput) (*mcp.CallToolResult, bookOutput, error) {
	modal, err := h.cp.ReservationModal(ctx, in.SlotID)
	if err != nil {
		return nil, bookOutput{}, err
	}

	if !creditsBookable(modal) {
		return nil, bookOutput{}, fmt.Errorf("slot %d cannot be booked with credits", in.SlotID)
	}

	credits := modal.Modal.Pricing.Credits

	uid, err := h.cp.UserID(ctx)
	if err != nil {
		return nil, bookOutput{}, err
	}

	balance, err := h.cp.CreditsRemaining(ctx, uid)
	if err != nil {
		return nil, bookOutput{}, err
	}

	if balance < credits {
		return nil, bookOutput{}, fmt.Errorf("slot %d costs %d credits but only %d remain", in.SlotID, credits, balance)
	}

	r, err := h.cp.Reserve(ctx, in.SlotID, credits)
	if err != nil {
		return nil, bookOutput{}, err
	}

	out := bookOutput{
		ReservationID:    r.ReservationID,
		CreditsCharged:   credits,
		CreditsRemaining: r.CreditsBalance.CreditsRemaining,
	}
	if len(modal.Modal.Policy.Explainers) > 0 {
		out.CancellationPolicy = modal.Modal.Policy.Explainers[0]
	}

	return nil, out, nil
}

// creditsBookable reports whether checkout offers an enabled pay-with-credits action.
func creditsBookable(m *classpass.ReservationModal) bool {
	return m.State == 0 && hasEnabledCTA(m.Modal.Footer.CTA, "credits")
}

func hasEnabledCTA(ctas []classpass.CTA, typ string) bool {
	for _, cta := range ctas {
		if cta.Type == typ && cta.Enabled {
			return true
		}
	}

	return false
}
