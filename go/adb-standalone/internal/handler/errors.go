package handler

import (
	"encoding/json"
	"net/http"
)

type errorEnvelope struct {
	Reason string `json:"reason"`
	Status string `json:"status"`
}

func writeError(w http.ResponseWriter, status int, reason string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorEnvelope{Reason: reason, Status: "error"})
}
