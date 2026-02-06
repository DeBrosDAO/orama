package rqlite

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"go.uber.org/zap"
)

// RQLiteClient is a simple HTTP client for RQLite
type RQLiteClient struct {
	baseURL    string
	httpClient *http.Client
	logger     *zap.Logger
}

// QueryResponse represents the RQLite query response
type QueryResponse struct {
	Results []QueryResult `json:"results"`
}

// QueryResult represents a single query result
type QueryResult struct {
	Columns []string        `json:"columns"`
	Types   []string        `json:"types"`
	Values  [][]interface{} `json:"values"`
	Error   string          `json:"error"`
}

// NewRQLiteClient creates a new RQLite HTTP client
func NewRQLiteClient(dsn string, logger *zap.Logger) (*RQLiteClient, error) {
	return &RQLiteClient{
		baseURL: dsn,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:        10,
				MaxIdleConnsPerHost: 10,
				IdleConnTimeout:     90 * time.Second,
			},
		},
		logger: logger,
	}, nil
}

// Query executes a SQL query and returns the results
func (c *RQLiteClient) Query(ctx context.Context, query string, args ...interface{}) ([][]interface{}, error) {
	// Build parameterized query
	queries := [][]interface{}{append([]interface{}{query}, args...)}

	reqBody, err := json.Marshal(queries)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal query: %w", err)
	}

	url := c.baseURL + "/db/query"
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("query failed with status %d: %s", resp.StatusCode, string(body))
	}

	var queryResp QueryResponse
	if err := json.NewDecoder(resp.Body).Decode(&queryResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	if len(queryResp.Results) == 0 {
		return [][]interface{}{}, nil
	}

	result := queryResp.Results[0]
	if result.Error != "" {
		return nil, fmt.Errorf("query error: %s", result.Error)
	}

	return result.Values, nil
}

// Close closes the HTTP client
func (c *RQLiteClient) Close() error {
	c.httpClient.CloseIdleConnections()
	return nil
}
