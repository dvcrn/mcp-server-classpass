package server

import (
	"context"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type meOutput struct {
	UserID               int64  `json:"user_id"`
	Name                 string `json:"name"`
	Email                string `json:"email"`
	Plan                 string `json:"plan"`
	PlanCredits          int    `json:"plan_credits"`
	CreditsRemaining     int    `json:"credits_remaining"`
	MembershipStatus     string `json:"membership_status"`
	CycleEnds            string `json:"cycle_ends"`
	UpcomingReservations int    `json:"upcoming_reservations"`
}

func (h *handlers) me(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, meOutput, error) {
	p, err := h.cp.Profile(ctx)
	if err != nil {
		return nil, meOutput{}, err
	}

	credits, err := h.cp.CreditsRemaining(ctx, p.ID)
	if err != nil {
		return nil, meOutput{}, err
	}

	return nil, meOutput{
		UserID:               p.ID,
		Name:                 p.FirstName + " " + p.LastName,
		Email:                p.Email,
		Plan:                 p.Membership.PlanName,
		PlanCredits:          p.Membership.PlanCredits,
		CreditsRemaining:     credits,
		MembershipStatus:     p.Membership.Status,
		CycleEnds:            time.Unix(p.Membership.CycleEnd, 0).UTC().Format(time.RFC3339),
		UpcomingReservations: p.ReservationInfo.UpcomingReservationCount,
	}, nil
}
