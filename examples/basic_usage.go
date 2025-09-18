package main

import (
	"context"
	"log"
	"time"

	"github.com/DeBrosOfficial/network/pkg/client"
)

func main() {
	// Create client configuration
	config := client.DefaultClientConfig("example_app")
	config.BootstrapPeers = []string{
		"/ip4/127.0.0.1/tcp/4001/p2p/QmBootstrap1",
	}

	// Create network client
	networkClient, err := client.NewClient(config)
	if err != nil {
		log.Fatalf("Failed to create network client: %v", err)
	}

	// Connect to network
	if err := networkClient.Connect(); err != nil {
		log.Fatalf("Failed to connect to network: %v", err)
	}
	defer networkClient.Disconnect()

	log.Printf("Connected to network successfully!")

	// Example: Database operations
	demonstrateDatabase(networkClient)

	// Example: Pub/Sub messaging
	demonstratePubSub(networkClient)

	// Example: Network information
	demonstrateNetworkInfo(networkClient)

	log.Printf("Example completed successfully!")
}

func demonstrateDatabase(client client.NetworkClient) {
	ctx := context.Background()
	db := client.Database()

	log.Printf("=== Database Operations ===")

	// Create a table
	schema := `
		CREATE TABLE IF NOT EXISTS messages (
			id INTEGER PRIMARY KEY,
			content TEXT NOT NULL,
			timestamp DATETIME DEFAULT CURRENT_TIMESTAMP
		)
	`
	if err := db.CreateTable(ctx, schema); err != nil {
		log.Printf("Error creating table: %v", err)
		return
	}
	log.Printf("Table created successfully")

	// Insert some data
	insertSQL := "INSERT INTO messages (content) VALUES (?)"
	result, err := db.Query(ctx, insertSQL, "Hello, distributed world!")
	if err != nil {
		log.Printf("Error inserting data: %v", err)
		return
	}
	log.Printf("Data inserted, result: %+v", result)

	// Query data
	selectSQL := "SELECT * FROM messages"
	result, err = db.Query(ctx, selectSQL)
	if err != nil {
		log.Printf("Error querying data: %v", err)
		return
	}
	log.Printf("Query result: %+v", result)
}

func demonstratePubSub(client client.NetworkClient) {
	ctx := context.Background()
	pubsub := client.PubSub()

	log.Printf("=== Pub/Sub Operations ===")

	// Subscribe to a topic
	topic := "notifications"
	handler := func(topic string, data []byte) error {
		log.Printf("Received message on topic '%s': %s", topic, string(data))
		return nil
	}

	if err := pubsub.Subscribe(ctx, topic, handler); err != nil {
		log.Printf("Error subscribing: %v", err)
		return
	}
	log.Printf("Subscribed to topic: %s", topic)

	// Publish a message
	message := []byte("Hello from pub/sub!")
	if err := pubsub.Publish(ctx, topic, message); err != nil {
		log.Printf("Error publishing: %v", err)
		return
	}
	log.Printf("Message published")

	// Wait a bit for message delivery
	time.Sleep(time.Millisecond * 100)

	// List topics
	topics, err := pubsub.ListTopics(ctx)
	if err != nil {
		log.Printf("Error listing topics: %v", err)
		return
	}
	log.Printf("Subscribed topics: %v", topics)
}

func demonstrateNetworkInfo(client client.NetworkClient) {
	ctx := context.Background()
	network := client.Network()

	log.Printf("=== Network Information ===")

	// Get network status
	status, err := network.GetStatus(ctx)
	if err != nil {
		log.Printf("Error getting status: %v", err)
		return
	}
	log.Printf("Network status: %+v", status)

	// Get peers
	peers, err := network.GetPeers(ctx)
	if err != nil {
		log.Printf("Error getting peers: %v", err)
		return
	}
	log.Printf("Connected peers: %+v", peers)

	// Get client health
	health, err := client.Health()
	if err != nil {
		log.Printf("Error getting health: %v", err)
		return
	}
	log.Printf("Client health: %+v", health)
}
