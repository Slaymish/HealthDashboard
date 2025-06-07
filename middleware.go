package main

import (
	"net/http"
	"strings"
)

func pinAuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Allow unauthenticated access for API endpoints and static assets.
		if strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/static/") || r.URL.Path == "/login" {
			next.ServeHTTP(w, r)
			return
		}

		// For UI pages, check the PIN cookie and redirect to login if missing or incorrect.
		c, err := r.Cookie("pin")
		if err != nil || c.Value != "1234" {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}

		next.ServeHTTP(w, r)
	})
}
