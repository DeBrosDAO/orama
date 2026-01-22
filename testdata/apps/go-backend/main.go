package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"time"
)

type User struct {
	ID        int       `json:"id"`
	Name      string    `json:"name"`
	Email     string    `json:"email"`
	CreatedAt time.Time `json:"created_at"`
}

type HealthResponse struct {
	Status    string    `json:"status"`
	Timestamp time.Time `json:"timestamp"`
	Service   string    `json:"service"`
}

type UsersResponse struct {
	Users []User `json:"users"`
	Total int    `json:"total"`
}

type CreateUserRequest struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

var users = []User{
	{ID: 1, Name: "Alice", Email: "alice@example.com", CreatedAt: time.Now()},
	{ID: 2, Name: "Bob", Email: "bob@example.com", CreatedAt: time.Now()},
	{ID: 3, Name: "Charlie", Email: "charlie@example.com", CreatedAt: time.Now()},
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(HealthResponse{
		Status:    "healthy",
		Timestamp: time.Now(),
		Service:   "go-backend-test",
	})
}

func usersHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	switch r.Method {
	case http.MethodGet:
		json.NewEncoder(w).Encode(UsersResponse{
			Users: users,
			Total: len(users),
		})

	case http.MethodPost:
		var req CreateUserRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}

		newUser := User{
			ID:        len(users) + 1,
			Name:      req.Name,
			Email:     req.Email,
			CreatedAt: time.Now(),
		}
		users = append(users, newUser)

		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"user":    newUser,
		})

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func rootHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Orama Network Go Backend Test",
		"version": "1.0.0",
		"endpoints": map[string]string{
			"health": "/health",
			"users":  "/api/users",
		},
	})
}

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	http.HandleFunc("/", rootHandler)
	http.HandleFunc("/health", healthHandler)
	http.HandleFunc("/api/users", usersHandler)

	log.Printf("Starting Go backend on port %s", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}
