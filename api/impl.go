package api

import (
	"encoding/json"
	"net/http"
	"oneTrick/services"
)

// ensure that we've conformed to the `ServerInterface` with a compile-time check
var _ ServerInterface = (*Server)(nil)

type Server struct {
	DestinyService services.DestinyService
}

func NewServer(service services.DestinyService) Server {
	return Server{
		DestinyService: service,
	}
}

// GetPing (GET /ping)
func (Server) GetPing(w http.ResponseWriter, r *http.Request) {
	resp := Pong{
		Ping: "pong",
	}

	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}
