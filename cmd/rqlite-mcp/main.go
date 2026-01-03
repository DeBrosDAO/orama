package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/rqlite/gorqlite"
)

// MCP JSON-RPC types
type JSONRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type JSONRPCResponse struct {
	JSONRPC string         `json:"jsonrpc"`
	ID      any            `json:"id"`
	Result  any            `json:"result,omitempty"`
	Error   *ResponseError `json:"error,omitempty"`
}

type ResponseError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Tool definition
type Tool struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	InputSchema any    `json:"inputSchema"`
}

// Tool call types
type CallToolRequest struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type TextContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type CallToolResult struct {
	Content []TextContent `json:"content"`
	IsError bool          `json:"isError,omitempty"`
}

type MCPServer struct {
	conn *gorqlite.Connection
}

func NewMCPServer(rqliteURL string) (*MCPServer, error) {
	conn, err := gorqlite.Open(rqliteURL)
	if err != nil {
		return nil, err
	}
	return &MCPServer{
		conn: conn,
	}, nil
}

func (s *MCPServer) handleRequest(req JSONRPCRequest) JSONRPCResponse {
	var resp JSONRPCResponse
	resp.JSONRPC = "2.0"
	resp.ID = req.ID

	log.Printf("Received method: %s", req.Method)

	switch req.Method {
	case "initialize":
		resp.Result = map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities": map[string]any{
				"tools": map[string]any{},
			},
			"serverInfo": map[string]any{
				"name":    "rqlite-mcp",
				"version": "0.1.0",
			},
		}

	case "notifications/initialized":
		// This is a notification, no response needed
		return JSONRPCResponse{}

	case "tools/list":
		log.Printf("Listing tools")
		tools := []Tool{
			{
				Name:        "list_tables",
				Description: "List all tables in the Rqlite database",
				InputSchema: map[string]any{
					"type":       "object",
					"properties": map[string]any{},
				},
			},
			{
				Name:        "query",
				Description: "Run a SELECT query on the Rqlite database",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"sql": map[string]any{
							"type":        "string",
							"description": "The SQL SELECT query to run",
						},
					},
					"required": []string{"sql"},
				},
			},
			{
				Name:        "execute",
				Description: "Run an INSERT, UPDATE, or DELETE statement on the Rqlite database",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"sql": map[string]any{
							"type":        "string",
							"description": "The SQL statement (INSERT, UPDATE, DELETE) to run",
						},
					},
					"required": []string{"sql"},
				},
			},
		}
		resp.Result = map[string]any{"tools": tools}

	case "tools/call":
		var callReq CallToolRequest
		if err := json.Unmarshal(req.Params, &callReq); err != nil {
			resp.Error = &ResponseError{Code: -32700, Message: "Parse error"}
			return resp
		}
		resp.Result = s.handleToolCall(callReq)

	default:
		log.Printf("Unknown method: %s", req.Method)
		resp.Error = &ResponseError{Code: -32601, Message: "Method not found"}
	}

	return resp
}

func (s *MCPServer) handleToolCall(req CallToolRequest) CallToolResult {
	log.Printf("Tool call: %s", req.Name)

	switch req.Name {
	case "list_tables":
		rows, err := s.conn.QueryOne("SELECT name FROM sqlite_master WHERE type='table' ORDER BY name")
		if err != nil {
			return errorResult(fmt.Sprintf("Error listing tables: %v", err))
		}
		var tables []string
		for rows.Next() {
			slice, err := rows.Slice()
			if err == nil && len(slice) > 0 {
				tables = append(tables, fmt.Sprint(slice[0]))
			}
		}
		if len(tables) == 0 {
			return textResult("No tables found")
		}
		return textResult(strings.Join(tables, "\n"))

	case "query":
		var args struct {
			SQL string `json:"sql"`
		}
		if err := json.Unmarshal(req.Arguments, &args); err != nil {
			return errorResult(fmt.Sprintf("Invalid arguments: %v", err))
		}
		log.Printf("Executing query: %s", args.SQL)
		rows, err := s.conn.QueryOne(args.SQL)
		if err != nil {
			return errorResult(fmt.Sprintf("Query error: %v", err))
		}

		var result strings.Builder
		cols := rows.Columns()
		result.WriteString(strings.Join(cols, " | ") + "\n")
		result.WriteString(strings.Repeat("-", len(cols)*10) + "\n")

		rowCount := 0
		for rows.Next() {
			vals, err := rows.Slice()
			if err != nil {
				continue
			}
			rowCount++
			for i, v := range vals {
				if i > 0 {
					result.WriteString(" | ")
				}
				result.WriteString(fmt.Sprint(v))
			}
			result.WriteString("\n")
		}
		result.WriteString(fmt.Sprintf("\n(%d rows)", rowCount))
		return textResult(result.String())

	case "execute":
		var args struct {
			SQL string `json:"sql"`
		}
		if err := json.Unmarshal(req.Arguments, &args); err != nil {
			return errorResult(fmt.Sprintf("Invalid arguments: %v", err))
		}
		log.Printf("Executing statement: %s", args.SQL)
		res, err := s.conn.WriteOne(args.SQL)
		if err != nil {
			return errorResult(fmt.Sprintf("Execution error: %v", err))
		}
		return textResult(fmt.Sprintf("Rows affected: %d", res.RowsAffected))

	default:
		return errorResult(fmt.Sprintf("Unknown tool: %s", req.Name))
	}
}

func textResult(text string) CallToolResult {
	return CallToolResult{
		Content: []TextContent{
			{
				Type: "text",
				Text: text,
			},
		},
	}
}

func errorResult(text string) CallToolResult {
	return CallToolResult{
		Content: []TextContent{
			{
				Type: "text",
				Text: text,
			},
		},
		IsError: true,
	}
}

func main() {
	// Log to stderr so stdout is clean for JSON-RPC
	log.SetOutput(os.Stderr)

	rqliteURL := "http://localhost:5001"
	if u := os.Getenv("RQLITE_URL"); u != "" {
		rqliteURL = u
	}

	var server *MCPServer
	var err error

	// Retry connecting to rqlite
	maxRetries := 30
	for i := 0; i < maxRetries; i++ {
		server, err = NewMCPServer(rqliteURL)
		if err == nil {
			break
		}
		if i%5 == 0 {
			log.Printf("Waiting for Rqlite at %s... (%d/%d)", rqliteURL, i+1, maxRetries)
		}
		time.Sleep(1 * time.Second)
	}

	if err != nil {
		log.Fatalf("Failed to connect to Rqlite after %d retries: %v", maxRetries, err)
	}

	log.Printf("MCP Rqlite server started (stdio transport)")
	log.Printf("Connected to Rqlite at %s", rqliteURL)

	// Read JSON-RPC requests from stdin, write responses to stdout
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		var req JSONRPCRequest
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			log.Printf("Failed to parse request: %v", err)
			continue
		}

		resp := server.handleRequest(req)

		// Don't send response for notifications (no ID)
		if req.ID == nil && strings.HasPrefix(req.Method, "notifications/") {
			continue
		}

		respData, err := json.Marshal(resp)
		if err != nil {
			log.Printf("Failed to marshal response: %v", err)
			continue
		}

		fmt.Println(string(respData))
	}

	if err := scanner.Err(); err != nil {
		log.Printf("Scanner error: %v", err)
	}
}
