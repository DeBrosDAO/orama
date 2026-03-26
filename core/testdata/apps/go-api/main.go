package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"

	_ "modernc.org/sqlite"
)

var db *sql.DB

type Note struct {
	ID        int    `json:"id"`
	Title     string `json:"title"`
	Content   string `json:"content"`
	CreatedAt string `json:"created_at"`
}

func cors(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == "OPTIONS" {
			w.WriteHeader(200)
			return
		}
		next(w, r)
	}
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok", "service": "go-api"})
}

func notesHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	switch r.Method {
	case "GET":
		rows, err := db.Query("SELECT id, title, content, created_at FROM notes ORDER BY id DESC")
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		defer rows.Close()

		notes := []Note{}
		for rows.Next() {
			var n Note
			rows.Scan(&n.ID, &n.Title, &n.Content, &n.CreatedAt)
			notes = append(notes, n)
		}
		json.NewEncoder(w).Encode(notes)

	case "POST":
		var n Note
		if err := json.NewDecoder(r.Body).Decode(&n); err != nil {
			http.Error(w, "invalid json", 400)
			return
		}
		result, err := db.Exec("INSERT INTO notes (title, content) VALUES (?, ?)", n.Title, n.Content)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		id, _ := result.LastInsertId()
		n.ID = int(id)
		w.WriteHeader(201)
		json.NewEncoder(w).Encode(n)

	case "DELETE":
		// DELETE /api/notes/123
		parts := strings.Split(r.URL.Path, "/")
		if len(parts) < 4 {
			http.Error(w, "id required", 400)
			return
		}
		id := parts[len(parts)-1]
		db.Exec("DELETE FROM notes WHERE id = ?", id)
		json.NewEncoder(w).Encode(map[string]string{"deleted": id})

	default:
		http.Error(w, "method not allowed", 405)
	}
}

func main() {
	var err error
	db, err = sql.Open("sqlite", "./data.db")
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	db.Exec(`CREATE TABLE IF NOT EXISTS notes (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		title TEXT NOT NULL,
		content TEXT NOT NULL,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	)`)

	http.HandleFunc("/health", cors(healthHandler))
	http.HandleFunc("/api/notes", cors(notesHandler))
	http.HandleFunc("/api/notes/", cors(notesHandler))

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	fmt.Printf("Go API listening on :%s\n", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}
