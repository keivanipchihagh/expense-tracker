package api

import "net/http"

type healthResponse struct {
	Status string `json:"status"`
}

// Health reports that the service is up. It doesn't say which version is
// running: the endpoint is public, and the version helps nobody but an
// attacker matching it against known issues.
func (s *Server) Health(w http.ResponseWriter, r *http.Request) {
	writeJson(w, http.StatusOK, healthResponse{Status: "ok"})
}
