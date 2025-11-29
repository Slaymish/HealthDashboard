package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// DateFormat is the standard date format used throughout the application.
const DateFormat = "2006-01-02"

// respondErr logs the given error and sends an HTTP error response including the details.
func respondErr(w http.ResponseWriter, status int, msg string, err error) {
	logger.Error(strings.ToLower(msg), "err", err)
	http.Error(w, fmt.Sprintf("%s: %v", msg, err), status)
}

// respondJSON writes a JSON response with the given status code and value.
func respondJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		logger.Error("json encode", "err", err)
	}
}
