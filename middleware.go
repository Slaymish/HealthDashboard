package main

import (
	"net/http"
	"strings"
)

func pinAuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Publicly accessible paths
		if r.URL.Path == "/login" || strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/static/") {
			next.ServeHTTP(w, r)
			return
		}

		// Only protect the main dashboard page
		if r.Method == http.MethodGet && r.URL.Path == "/" {
			c, err := r.Cookie("pin")
			if err != nil || c.Value != "1234" {
				http.Redirect(w, r, "/login", http.StatusSeeOther)
				return
			}
		}

		next.ServeHTTP(w, r)
	})
}
