package handler

import (
	"net/http"

	"studdle/backend/internal/authctx"
	"studdle/backend/internal/httpx"
	"studdle/backend/pkg/gamification"
)

// GamificationHandler exposes gamification endpoints.
type GamificationHandler struct {
	svc *gamification.Service // svc owns gamification logic
}

// NewGamificationHandler constructs a GamificationHandler.
func NewGamificationHandler(svc *gamification.Service) *GamificationHandler {
	return &GamificationHandler{svc: svc}
}

// State handles GET /gamification-state.
func (h *GamificationHandler) State(w http.ResponseWriter, r *http.Request) {
	uid := authctx.UID(r.Context())
	streak, goal, err := h.svc.GetState(r.Context(), uid)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"streak":     streak,
		"daily_goal": goal,
	})
}

// RecordSession handles POST /training-session-record.
func (h *GamificationHandler) RecordSession(w http.ResponseWriter, r *http.Request) {
	uid := authctx.UID(r.Context())
	var in gamification.RecordSessionInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		httpx.WriteError(w, err)
		return
	}
	res, err := h.svc.RecordSession(r.Context(), uid, in)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

// Stats handles GET /user-stats.
func (h *GamificationHandler) Stats(w http.ResponseWriter, r *http.Request) {
	uid := authctx.UID(r.Context())
	stats, err := h.svc.GetUserStats(r.Context(), uid)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, stats)
}

// Achievements handles GET /achievements.
func (h *GamificationHandler) Achievements(w http.ResponseWriter, r *http.Request) {
	uid := authctx.UID(r.Context())
	list, err := h.svc.ListAchievements(r.Context(), uid)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, list)
}
