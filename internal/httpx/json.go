package httpx

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"studdle/backend/internal/myErrors"
)

// DecodeJSON parses the request body into dst. Returns ErrInvalidInput on failure.
func DecodeJSON(r *http.Request, dst any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return fmt.Errorf("decode json:\n%w", errors.Join(myErrors.ErrInvalidInput, err))
	}
	return nil
}

// WriteJSON writes a 200/custom-status JSON response.
func WriteJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
