package config

import (
	"fmt"
	"os"

	"github.com/joho/godotenv"
)

type Config struct {
	Port         string
	DBPath       string
	JWTSecret    string
	HeliusAPIKey string
	HeliusRPCURL string
}

func Load() (*Config, error) {
	// Load .env file if it exists (ignore error if missing)
	_ = godotenv.Load()

	cfg := &Config{
		Port:         envOrDefault("PORT", "8090"),
		DBPath:       envOrDefault("DB_PATH", "invest.db"),
		JWTSecret:    os.Getenv("JWT_SECRET"),
		HeliusAPIKey: os.Getenv("HELIUS_API_KEY"),
		HeliusRPCURL: os.Getenv("HELIUS_RPC_URL"),
	}

	if cfg.JWTSecret == "" {
		return nil, fmt.Errorf("JWT_SECRET environment variable is required")
	}

	if cfg.HeliusAPIKey == "" {
		return nil, fmt.Errorf("HELIUS_API_KEY environment variable is required")
	}

	if cfg.HeliusRPCURL == "" {
		cfg.HeliusRPCURL = "https://mainnet.helius-rpc.com/?api-key=" + cfg.HeliusAPIKey
	}

	return cfg, nil
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
