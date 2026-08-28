package http

import (
	"encoding/json"
	"net/http"
)

// writeJSON encodes v as JSON and writes it with the given HTTP status.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError uses a single consistent error envelope for this service's
// own errors.
func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
