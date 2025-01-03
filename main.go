package main

import (
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"log"
	"log/slog"
	"net/http"
	"oneTrick/api"
	"oneTrick/services"
	"time"
)

const primaryMembershipId = 4611686018434106050

func main() {
	destinyService := services.NewDestinyService()
	server := api.NewServer(destinyService)

	r := chi.NewMux()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	// Basic CORS
	// for more ideas, see: https://developer.github.com/v3/#cross-origin-resource-sharing
	r.Use(cors.Handler(cors.Options{
		// AllowedOrigins:   []string{"https://foo.com"}, // Use this to allow specific origin hosts
		AllowedOrigins: []string{"https://*", "http://*"},
		// AllowOriginFunc:  func(r *http.Request, origin string) bool { return true },
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-CSRF-Token"},
		ExposedHeaders:   []string{"Link"},
		AllowCredentials: false,
		MaxAge:           300, // Maximum value not ignored by any of major browsers
	}))

	// Set a timeout value on the request context (ctx), that will signal
	// through ctx.Done() that the request has timed out and further
	// processing should be stopped.
	r.Use(middleware.Timeout(60 * time.Second))

	h := api.HandlerFromMux(server, r)

	r.Get("/profile", func(w http.ResponseWriter, r *http.Request) {
		items, timestamp, err := destinyService.GetUserSnapshot(primaryMembershipId)
		if err != nil {
			http.Error(w, "Failed to fetch profile data", http.StatusInternalServerError)
			return
		}

		err = destinyService.WriteToFile(items, timestamp)
		if err != nil {
			http.Error(w, "Failed to save profile data", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("Profile data saved successfully!"))
	})
	s := &http.Server{
		Handler: h,
		Addr:    "0.0.0.0:8080",
	}

	slog.Info("Starting HTTP server on port 8080")
	log.Fatal(s.ListenAndServe())
}
