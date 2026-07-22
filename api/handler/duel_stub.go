package handler

import (
	"net/http"

	"studdle/backend/internal/httpx"
	"studdle/backend/internal/myErrors"
	"studdle/backend/pkg/duel"
)

// DuelHandler stubs Spec E endpoints.
type DuelHandler struct {
	svc *duel.Service // svc is the (stub) duel service
}

// NewDuelHandler constructs a DuelHandler.
func NewDuelHandler(svc *duel.Service) *DuelHandler { return &DuelHandler{svc: svc} }

// Invite stubs POST /duel/invite.
func (h *DuelHandler) Invite(w http.ResponseWriter, r *http.Request) {
	httpx.WriteError(w, myErrors.ErrNotImplemented)
}

// Accept stubs POST /duel/accept?id=...
func (h *DuelHandler) Accept(w http.ResponseWriter, r *http.Request) {
	httpx.WriteError(w, myErrors.ErrNotImplemented)
}

// Connect stubs GET /duel/connect?id=... (WebSocket upgrade in Spec E).
func (h *DuelHandler) Connect(w http.ResponseWriter, r *http.Request) {
	httpx.WriteError(w, myErrors.ErrNotImplemented)
}
