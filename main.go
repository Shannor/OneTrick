package main

import (
	"log/slog"
	"net/http"
	"oneTrick/libs"
)

const primaryMembershipId = 4611686018434106050

func main() {
	http.HandleFunc("/ping", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("pong"))
	})

	http.HandleFunc("/profile", func(w http.ResponseWriter, r *http.Request) {
		api := libs.NewDestinyAPI()
		items, timestamp, err := api.GetUserSnapshot(primaryMembershipId)
		if err != nil {
			http.Error(w, "Failed to fetch profile data", http.StatusInternalServerError)
			return
		}

		err = api.WriteToFile(items, timestamp)
		if err != nil {
			http.Error(w, "Failed to save profile data", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("Profile data saved successfully!"))
	})

	slog.Info("Starting HTTP server on port 8080")
	if err := http.ListenAndServe(":8080", nil); err != nil {
		slog.Error("HTTP server failed: ", err)
	}
}
