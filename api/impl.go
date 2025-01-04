package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"oneTrick/services"
	"strconv"
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

func (Server) GetPing(w http.ResponseWriter, r *http.Request) {
	resp := Pong{
		Ping: "pong",
	}

	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

const characterID = "2305843009261519028"
const primaryMembershipId = 4611686018434106050

func (s Server) GetWeaponsForActivity(w http.ResponseWriter, r *http.Request, activityId string) {
	id, err := strconv.ParseInt(activityId, 10, 64)
	if err != nil {
		http.Error(w, "Invalid activity ID", http.StatusBadRequest)
		return
	}
	var results []WeaponStats
	resp, period, err := s.DestinyService.GetWeaponsFromActivity(r.Context(), characterID, id)
	if err != nil {
		http.Error(w, "Failed to fetch activity data", http.StatusInternalServerError)
	}
	// 1. Get the closet snapshot(s)
	components, err := s.DestinyService.GetClosestSnapshot(primaryMembershipId, period)
	if err != nil {
		http.Error(w, "Failed to get a inventory snapshot", http.StatusInternalServerError)
		return
	}

	// 2 Compare refs with itemHash.
	mapping := map[int64]string{}
	for _, component := range components {
		mapping[int64(*component.ItemHash)] = *component.ItemInstanceId
	}
	// 3. Get weapon data
	for _, stats := range resp {
		result := WeaponStats{}
		id, ok := mapping[int64(*stats.ReferenceId)]
		if !ok {
			slog.Warn("No instance id found for reference id: ", *stats.ReferenceId)
			continue
		}
		result.ReferenceId = stats.ReferenceId
		result.Stats = TransformD2HistoricalStatValues(stats.Values)
		details, err := s.DestinyService.GetWeaponDetails(r.Context(), strconv.Itoa(primaryMembershipId), id)
		if err != nil {
			slog.With(
				"error",
				err.Error(),
				"weapon instance id",
				id,
			).Error("failed to get details for weapon")
		}
		result.ItemDetails = TransformItemToDetails(details)
		results = append(results, result)
	}

	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(results)
}
