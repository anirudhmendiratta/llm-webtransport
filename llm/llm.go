package llm

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

type chatRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
	Stream   bool          `json:"stream"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatChunk struct {
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
	} `json:"choices"`
	Usage *chatUsage `json:"usage,omitempty"`
}

type chatUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}

type Stats struct {
	BytesReceived    int // total content bytes received from LLM (excluding reasoning)
	BytesSent        int // total content bytes sent to client
	PromptTokens     int
	CompletionTokens int
}

// StreamChatCompletion sends a message to the OpenAI-compatible API and calls
// onToken for each streamed token. It returns stats and any error.
func StreamChatCompletion(baseURL, model, userMessage string, onToken func(token string) error) (Stats, error) {
	var stats Stats

	reqBody := chatRequest{
		Model: model,
		Messages: []chatMessage{
			{Role: "user", Content: userMessage},
		},
		Stream: true,
	}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return stats, fmt.Errorf("marshal request: %w", err)
	}

	resp, err := http.Post(baseURL+"/v1/chat/completions", "application/json", bytes.NewReader(body))
	if err != nil {
		return stats, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return stats, fmt.Errorf("API error %d: %s", resp.StatusCode, b)
	}

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" || line == "data: [DONE]" {
			continue
		}
		const prefix = "data: "
		if len(line) <= len(prefix) {
			continue
		}
		data := line[len(prefix):]

		var chunk chatChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}
		if chunk.Usage != nil {
			stats.PromptTokens = chunk.Usage.PromptTokens
			stats.CompletionTokens = chunk.Usage.CompletionTokens
		}
		if len(chunk.Choices) > 0 && chunk.Choices[0].Delta.Content != "" {
			token := chunk.Choices[0].Delta.Content
			stats.BytesReceived += len(line) + 1 // count only content-bearing lines
			stats.BytesSent += len(token)
			if err := onToken(token); err != nil {
				return stats, err
			}
		}
	}
	return stats, scanner.Err()
}
