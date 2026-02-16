package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

type chatRequest struct {
	Message string `json:"message"`
}

func main() {
	scanner := bufio.NewScanner(os.Stdin)
	fmt.Println("Connected. Type a message and press Enter to send. Ctrl+C to quit.")

	for {
		fmt.Print("> ")
		if !scanner.Scan() {
			break
		}
		text := scanner.Text()
		if text == "" {
			continue
		}

		body, _ := json.Marshal(chatRequest{Message: text})
		resp, err := http.Post("http://localhost:8080/chat", "application/json", bytes.NewReader(body))
		if err != nil {
			log.Printf("request failed: %v", err)
			continue
		}

		sendTime := time.Now()
		var ttft time.Duration
		tokenCount := 0
		var lastTokenTime time.Time
		var totalInterTokenTime time.Duration

		sseScanner := bufio.NewScanner(resp.Body)
		for sseScanner.Scan() {
			line := sseScanner.Text()
			if line == "" {
				continue
			}
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			data := line[len("data: "):]
			if data == "[DONE]" {
				break
			}

			now := time.Now()
			tokenCount++
			if tokenCount == 1 {
				ttft = now.Sub(sendTime)
			} else {
				totalInterTokenTime += now.Sub(lastTokenTime)
			}
			lastTokenTime = now
			fmt.Print(data)
		}
		resp.Body.Close()
		fmt.Println()

		if tokenCount > 0 {
			var avgTBT time.Duration
			if tokenCount > 1 {
				avgTBT = totalInterTokenTime / time.Duration(tokenCount-1)
			}
			fmt.Printf("[TTFT: %s | tokens: %d | avg TBT: %s]\n", ttft, tokenCount, avgTBT)
		}
	}
}
