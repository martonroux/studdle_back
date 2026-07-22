package middleware

import (
	"net/http"
	"strings"

	"studdle/backend/internal/authctx"
	"studdle/backend/internal/httpx"
	jwtsigner "studdle/backend/internal/jwt"
	"studdle/backend/internal/myErrors"
)

// Auth parses the Bearer token and attaches identity to the request context.
// Requests without a token are rejected with 401.
func Auth(s *jwtsigner.Signer) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			header := r.Header.Get("Authorization")
			if !strings.HasPrefix(header, "Bearer ") {
				httpx.WriteError(w, myErrors.ErrUnauthenticated)
				return
			}
			claims, err := s.Verify(strings.TrimPrefix(header, "Bearer "))
			if err != nil {
				httpx.WriteError(w, myErrors.ErrUnauthenticated)
				return
			}
			ctx := authctx.WithIdentity(r.Context(), claims.UID, claims.EmailVerified, claims.IsAdmin)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
