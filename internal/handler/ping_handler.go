package handler

import "net/http"

// Ping is a liveness check.
func (h *Handlers) Ping(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("pong"))
}
