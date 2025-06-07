package main

import (
	"fmt"
	"net/http"
	"strings"
)

// respondErr logs the given error and sends an HTTP error response including the details.
func respondErr(w http.ResponseWriter, status int, msg string, err error) {
	logger.Error(strings.ToLower(msg), "err", err)
	http.Error(w, fmt.Sprintf("%s: %v", msg, err), status)
}
