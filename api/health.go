package api

import "net/http"

type healthResponse struct {
	Status  string `json:"status"`
	Version string `json:"version"`
}

// Health reports that the service is up and which version is running.
func (s *Server) Health(w http.ResponseWriter, r *http.Request) {
	writeJson(w, http.StatusOK, healthResponse{Status: "ok", Version: s.version})
}
