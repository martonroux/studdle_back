package httpx

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"studbud/backend/internal/myErrors"
)

func TestWriteErrorMapsSentinels(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"unauthed", myErrors.ErrUnauthenticated, 401},
		{"not verified", myErrors.ErrNotVerified, 403},
		{"not found", myErrors.ErrNotFound, 404},
		{"conflict", myErrors.ErrConflict, 409},
		{"validation", myErrors.ErrValidation, 400},
		{"no ai access", myErrors.ErrNoAIAccess, 402},
		{"quota", myErrors.ErrQuotaExhausted, 429},
		{"not impl", myErrors.ErrNotImplemented, 501},
		{"stripe", myErrors.ErrStripe, 502},
		{"rate limited", myErrors.ErrRateLimited, 429},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			WriteError(rec, c.err)
			if rec.Code != c.want {
				t.Fatalf("status = %d, want %d", rec.Code, c.want)
			}
			var body map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("bad json: %v", err)
			}
			if _, ok := body["error"]; !ok {
				t.Fatalf("body missing 'error' key: %s", rec.Body.String())
			}
		})
	}
}

func TestWriteErrorAppErrorOverridesStatus(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteError(rec, &myErrors.AppError{Code: "x", Message: "y", Status: 418, Wrapped: myErrors.ErrValidation})
	if rec.Code != 418 {
		t.Fatalf("status = %d, want 418", rec.Code)
	}
}

func TestWriteError_ContentPolicy(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteError(rec, myErrors.ErrContentPolicy)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422", rec.Code)
	}
	var body struct{ Error struct{ Code string } }
	_ = json.NewDecoder(rec.Body).Decode(&body)
	if body.Error.Code != "content_policy" {
		t.Errorf("code = %q, want content_policy", body.Error.Code)
	}
}
