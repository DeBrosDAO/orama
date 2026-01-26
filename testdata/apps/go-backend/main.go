package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

type User struct {
	ID        int       `json:"id"`
	Name      string    `json:"name"`
	Email     string    `json:"email"`
	CreatedAt time.Time `json:"created_at"`
}

type HealthResponse struct {
	Status       string    `json:"status"`
	Timestamp    time.Time `json:"timestamp"`
	Service      string    `json:"service"`
	DatabaseName string    `json:"database_name,omitempty"`
	GatewayURL   string    `json:"gateway_url,omitempty"`
}

type UsersResponse struct {
	Users []User `json:"users"`
	Total int    `json:"total"`
}

type CreateUserRequest struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

// In-memory storage (used when DATABASE_NAME is not set)
var users = []User{
	{ID: 1, Name: "Alice", Email: "alice@example.com", CreatedAt: time.Now()},
	{ID: 2, Name: "Bob", Email: "bob@example.com", CreatedAt: time.Now()},
	{ID: 3, Name: "Charlie", Email: "charlie@example.com", CreatedAt: time.Now()},
}
var nextID = 4

// Environment variables
var (
	databaseName = os.Getenv("DATABASE_NAME")
	gatewayURL   = os.Getenv("GATEWAY_URL")
	apiKey       = os.Getenv("API_KEY")
)

// executeSQL executes a SQL query against the hosted SQLite database
func executeSQL(query string, args ...interface{}) ([]map[string]interface{}, error) {
	if databaseName == "" || gatewayURL == "" {
		return nil, fmt.Errorf("database not configured")
	}

	// Build the query with parameters
	reqBody := map[string]interface{}{
		"sql":    query,
		"params": args,
	}
	bodyBytes, _ := json.Marshal(reqBody)

	url := fmt.Sprintf("%s/v1/db/%s/query", gatewayURL, databaseName)
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(bodyBytes))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("database error: %s", string(body))
	}

	var result struct {
		Rows    []map[string]interface{} `json:"rows"`
		Columns []string                 `json:"columns"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	return result.Rows, nil
}

// initDatabase creates the users table if it doesn't exist
func initDatabase() error {
	if databaseName == "" || gatewayURL == "" {
		log.Printf("DATABASE_NAME or GATEWAY_URL not set, using in-memory storage")
		return nil
	}

	query := `CREATE TABLE IF NOT EXISTS users (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL,
		email TEXT NOT NULL,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	)`

	_, err := executeSQL(query)
	if err != nil {
		// Log but don't fail - the table might already exist
		log.Printf("Warning: Could not create users table: %v", err)
	} else {
		log.Printf("Users table initialized")
	}
	return nil
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(HealthResponse{
		Status:       "healthy",
		Timestamp:    time.Now(),
		Service:      "go-backend-test",
		DatabaseName: databaseName,
		GatewayURL:   gatewayURL,
	})
}

func usersHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Check if database is configured
	useDatabase := databaseName != "" && gatewayURL != ""

	switch r.Method {
	case http.MethodGet:
		if useDatabase {
			// Query from hosted SQLite
			rows, err := executeSQL("SELECT id, name, email, created_at FROM users ORDER BY id")
			if err != nil {
				log.Printf("Database query error: %v", err)
				http.Error(w, "Database error", http.StatusInternalServerError)
				return
			}

			var dbUsers []User
			for _, row := range rows {
				user := User{
					ID:    int(row["id"].(float64)),
					Name:  row["name"].(string),
					Email: row["email"].(string),
				}
				if ct, ok := row["created_at"].(string); ok {
					user.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", ct)
				}
				dbUsers = append(dbUsers, user)
			}

			json.NewEncoder(w).Encode(UsersResponse{
				Users: dbUsers,
				Total: len(dbUsers),
			})
		} else {
			// Use in-memory storage
			json.NewEncoder(w).Encode(UsersResponse{
				Users: users,
				Total: len(users),
			})
		}

	case http.MethodPost:
		var req CreateUserRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}

		if req.Name == "" || req.Email == "" {
			http.Error(w, "Name and email are required", http.StatusBadRequest)
			return
		}

		if useDatabase {
			// Insert into hosted SQLite
			_, err := executeSQL("INSERT INTO users (name, email) VALUES (?, ?)", req.Name, req.Email)
			if err != nil {
				log.Printf("Database insert error: %v", err)
				http.Error(w, "Database error", http.StatusInternalServerError)
				return
			}

			// Get the inserted user (last insert ID)
			rows, err := executeSQL("SELECT id, name, email, created_at FROM users WHERE name = ? AND email = ? ORDER BY id DESC LIMIT 1", req.Name, req.Email)
			if err != nil || len(rows) == 0 {
				http.Error(w, "Failed to retrieve created user", http.StatusInternalServerError)
				return
			}

			newUser := User{
				ID:    int(rows[0]["id"].(float64)),
				Name:  rows[0]["name"].(string),
				Email: rows[0]["email"].(string),
			}
			if ct, ok := rows[0]["created_at"].(string); ok {
				newUser.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", ct)
			}

			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": true,
				"user":    newUser,
			})
		} else {
			// Use in-memory storage
			newUser := User{
				ID:        nextID,
				Name:      req.Name,
				Email:     req.Email,
				CreatedAt: time.Now(),
			}
			nextID++
			users = append(users, newUser)

			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": true,
				"user":    newUser,
			})
		}

	case http.MethodDelete:
		// Parse user ID from query string (e.g., /api/users?id=1)
		idStr := r.URL.Query().Get("id")
		if idStr == "" {
			http.Error(w, "User ID required", http.StatusBadRequest)
			return
		}

		var id int
		fmt.Sscanf(idStr, "%d", &id)

		if useDatabase {
			_, err := executeSQL("DELETE FROM users WHERE id = ?", id)
			if err != nil {
				log.Printf("Database delete error: %v", err)
				http.Error(w, "Database error", http.StatusInternalServerError)
				return
			}
		} else {
			// Delete from in-memory storage
			for i, u := range users {
				if u.ID == id {
					users = append(users[:i], users[i+1:]...)
					break
				}
			}
		}

		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"message": "User deleted",
		})

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func rootHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	storageType := "in-memory"
	if databaseName != "" && gatewayURL != "" {
		storageType = "hosted-sqlite"
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Orama Network Go Backend Test",
		"version": "1.0.0",
		"storage": storageType,
		"endpoints": map[string]string{
			"health": "GET /health",
			"users":  "GET/POST/DELETE /api/users",
		},
		"config": map[string]string{
			"database_name": maskIfSet(databaseName),
			"gateway_url":   maskIfSet(gatewayURL),
		},
	})
}

func maskIfSet(s string) string {
	if s == "" {
		return "[not configured]"
	}
	if strings.Contains(s, "://") {
		// Mask URL partially
		return s[:strings.Index(s, "://")+3] + "..."
	}
	return "[configured]"
}

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	// Initialize database if configured
	if err := initDatabase(); err != nil {
		log.Printf("Warning: Database initialization failed: %v", err)
	}

	http.HandleFunc("/", rootHandler)
	http.HandleFunc("/health", healthHandler)
	http.HandleFunc("/api/users", usersHandler)

	log.Printf("Starting Go backend on port %s", port)
	log.Printf("Database: %s", maskIfSet(databaseName))
	log.Printf("Gateway: %s", maskIfSet(gatewayURL))
	log.Fatal(http.ListenAndServe(":"+port, nil))
}
