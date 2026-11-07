package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
)

// writeJson writes v as a JSON response with the given status code.
func writeJson(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("failed to encode json response", "error", err)
	}
}
