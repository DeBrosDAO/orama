package inspector

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// AnalysisResult holds the AI's analysis of check failures.
type AnalysisResult struct {
	Model    string
	Analysis string
	Duration time.Duration
}

// Analyze sends failures and cluster context to OpenRouter for AI analysis.
func Analyze(results *Results, data *ClusterData, model, apiKey string) (*AnalysisResult, error) {
	if apiKey == "" {
		apiKey = os.Getenv("OPENROUTER_API_KEY")
	}
	if apiKey == "" {
		return nil, fmt.Errorf("no API key: set --api-key or OPENROUTER_API_KEY env")
	}

	// Build the prompt with failures, warnings, and cluster context
	prompt := buildAnalysisPrompt(results, data)

	start := time.Now()
	response, err := callOpenRouter(model, apiKey, prompt)
	if err != nil {
		return nil, fmt.Errorf("OpenRouter API call failed: %w", err)
	}

	return &AnalysisResult{
		Model:    model,
		Analysis: response,
		Duration: time.Since(start),
	}, nil
}

func buildAnalysisPrompt(results *Results, data *ClusterData) string {
	var b strings.Builder

	// System context
	b.WriteString("You are a distributed systems expert analyzing health check results for an Orama Network cluster.\n")
	b.WriteString("The cluster runs RQLite (Raft consensus), Olric (distributed cache), IPFS, CoreDNS, and WireGuard.\n\n")

	// Cluster overview
	b.WriteString("## Cluster Overview\n")
	b.WriteString(fmt.Sprintf("Nodes inspected: %d\n", len(data.Nodes)))
	for host, nd := range data.Nodes {
		b.WriteString(fmt.Sprintf("- %s (role: %s)\n", host, nd.Node.Role))
	}
	b.WriteString("\n")

	// Summary
	passed, failed, warned, skipped := results.Summary()
	b.WriteString(fmt.Sprintf("## Check Results: %d passed, %d failed, %d warnings, %d skipped\n\n", passed, failed, warned, skipped))

	// List all failures
	failures := results.Failures()
	if len(failures) > 0 {
		b.WriteString("## Failures (CRITICAL)\n")
		for _, f := range failures {
			node := f.Node
			if node == "" {
				node = "cluster-wide"
			}
			b.WriteString(fmt.Sprintf("- [%s] %s (%s): %s\n", f.Severity, f.Name, node, f.Message))
		}
		b.WriteString("\n")
	}

	// List all warnings
	warnings := results.FailuresAndWarnings()
	warningsOnly := make([]CheckResult, 0)
	for _, w := range warnings {
		if w.Status == StatusWarn {
			warningsOnly = append(warningsOnly, w)
		}
	}
	if len(warningsOnly) > 0 {
		b.WriteString("## Warnings\n")
		for _, w := range warningsOnly {
			node := w.Node
			if node == "" {
				node = "cluster-wide"
			}
			b.WriteString(fmt.Sprintf("- [%s] %s (%s): %s\n", w.Severity, w.Name, node, w.Message))
		}
		b.WriteString("\n")
	}

	// Add raw RQLite status for context (condensed)
	b.WriteString("## Raw Cluster Data (condensed)\n")
	for host, nd := range data.Nodes {
		if nd.RQLite != nil && nd.RQLite.Status != nil {
			s := nd.RQLite.Status
			b.WriteString(fmt.Sprintf("### %s (RQLite)\n", host))
			b.WriteString(fmt.Sprintf("  raft_state=%s term=%d applied=%d commit=%d leader=%s peers=%d voter=%v\n",
				s.RaftState, s.Term, s.AppliedIndex, s.CommitIndex, s.LeaderNodeID, s.NumPeers, s.Voter))
			if nd.RQLite.Nodes != nil {
				b.WriteString(fmt.Sprintf("  /nodes reports %d members:", len(nd.RQLite.Nodes)))
				for addr, n := range nd.RQLite.Nodes {
					reachable := "ok"
					if !n.Reachable {
						reachable = "UNREACHABLE"
					}
					leader := ""
					if n.Leader {
						leader = " LEADER"
					}
					b.WriteString(fmt.Sprintf(" %s(%s%s)", addr, reachable, leader))
				}
				b.WriteString("\n")
			}
		}
	}

	b.WriteString("\n## Task\n")
	b.WriteString("Analyze the failures and warnings above. For each issue:\n")
	b.WriteString("1. Explain the root cause\n")
	b.WriteString("2. Assess the severity and impact on the cluster\n")
	b.WriteString("3. Suggest specific commands or actions to fix it\n")
	b.WriteString("\nBe concise and actionable. Group related issues together. Use markdown formatting.\n")

	return b.String()
}

// OpenRouter API types (OpenAI-compatible)

type openRouterRequest struct {
	Model    string              `json:"model"`
	Messages []openRouterMessage `json:"messages"`
}

type openRouterMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openRouterResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
		Code    int    `json:"code"`
	} `json:"error"`
}

func callOpenRouter(model, apiKey, prompt string) (string, error) {
	reqBody := openRouterRequest{
		Model: model,
		Messages: []openRouterMessage{
			{Role: "user", Content: prompt},
		},
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequest("POST", "https://openrouter.ai/api/v1/chat/completions", bytes.NewReader(jsonBody))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("HTTP request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("API returned %d: %s", resp.StatusCode, string(body))
	}

	var orResp openRouterResponse
	if err := json.Unmarshal(body, &orResp); err != nil {
		return "", fmt.Errorf("unmarshal response: %w", err)
	}

	if orResp.Error != nil {
		return "", fmt.Errorf("API error: %s", orResp.Error.Message)
	}

	if len(orResp.Choices) == 0 {
		return "", fmt.Errorf("no choices in response (raw: %s)", truncate(string(body), 500))
	}

	content := orResp.Choices[0].Message.Content
	if strings.TrimSpace(content) == "" {
		return "", fmt.Errorf("model returned empty response (raw: %s)", truncate(string(body), 500))
	}

	return content, nil
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

// PrintAnalysis writes the AI analysis to the output.
func PrintAnalysis(analysis *AnalysisResult, w io.Writer) {
	fmt.Fprintf(w, "\n## AI Analysis (%s)\n", analysis.Model)
	fmt.Fprintf(w, "%s\n", strings.Repeat("-", 70))
	fmt.Fprintf(w, "%s\n", analysis.Analysis)
	fmt.Fprintf(w, "\n(Analysis took %.1fs)\n", analysis.Duration.Seconds())
}
