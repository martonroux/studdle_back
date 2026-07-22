package httpx

import (
	"net/http"
	"strconv"

	"studdle/backend/internal/myErrors"
)

// QueryInt64 reads a required int64 query param by name.
// Returns ErrInvalidInput if the param is missing or not parseable.
func QueryInt64(r *http.Request, name string) (int64, error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return 0, myErrors.ErrInvalidInput
	}
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, myErrors.ErrInvalidInput
	}
	return v, nil
}

// QueryIntDefault reads an optional int query param, falling back to def when
// missing or unparseable.
func QueryIntDefault(r *http.Request, name string, def int) int {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return def
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return def
	}
	return v
}

// QueryBoolDefault reads an optional bool query param. Accepts the standard
// strconv.ParseBool truthy/falsy forms ("1", "true", "0", "false", ...).
// Returns def when missing or unparseable.
func QueryBoolDefault(r *http.Request, name string, def bool) bool {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return def
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		return def
	}
	return v
}
