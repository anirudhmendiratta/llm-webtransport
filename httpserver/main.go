package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	"llm-webtransport/llm"
)

type chatRequest struct {
	Message string `json:"message"`
}

func handleChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req chatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	if req.Message == "" {
		http.Error(w, "message is required", http.StatusBadRequest)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	inputBytes := len(req.Message)
	log.Printf("received: %s (%d bytes)", req.Message, inputBytes)

	stats, err := llm.StreamChatCompletion("http://127.0.0.1:11434", "gemma3:12b", req.Message, func(token string) error {
		_, err := fmt.Fprintf(w, "data: %s\n\n", token)
		if err != nil {
			return err
		}
		flusher.Flush()
		return nil
	})
	if err != nil {
		log.Printf("llm error: %v", err)
		fmt.Fprintf(w, "data: \n[error: %s]\n\n", err.Error())
		flusher.Flush()
	}

	fmt.Fprint(w, "data: [DONE]\n\n")
	flusher.Flush()

	log.Printf("stats: input=%d bytes, from_llm=%d bytes, to_client=%d bytes, prompt_tokens=%d, completion_tokens=%d",
		inputBytes, stats.BytesReceived, stats.BytesSent, stats.PromptTokens, stats.CompletionTokens)
}

func main() {
	http.HandleFunc("/chat", handleChat)
	log.Println("HTTP SSE server listening on :8080 (TLS)")
	if err := http.ListenAndServeTLS(":8080", "certs/cert.pem", "certs/key.pem", nil); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
