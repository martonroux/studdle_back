package middleware

import (
	"net/http"

	"studdle/backend/internal/authctx"
	"studdle/backend/internal/httpx"
	"studdle/backend/internal/myErrors"
)

// RequireVerified rejects requests whose JWT does not carry email_verified=true.
func RequireVerified() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !authctx.Verified(r.Context()) {
				httpx.WriteError(w, myErrors.ErrNotVerified)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
