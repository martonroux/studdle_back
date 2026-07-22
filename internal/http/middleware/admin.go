package middleware

import (
	"net/http"

	"studdle/backend/internal/authctx"
	"studdle/backend/internal/httpx"
	"studdle/backend/internal/myErrors"
)

// RequireAdmin rejects requests whose caller is not an admin.
func RequireAdmin() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !authctx.Admin(r.Context()) {
				httpx.WriteError(w, myErrors.ErrAdminRequired)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
