package main

import (
	"bufio"
	"context"
	"io"
	"log"
	"net/http"

	"llm-webtransport/llm"
	"llm-webtransport/message"

	"github.com/quic-go/webtransport-go"
)

type serverConfig struct {
	llmBaseURL string
	llmModel   string
}

func handleHttpToWebTransportUpgrade(s *webtransport.Server, cfg serverConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		session, err := s.Upgrade(w, r)
		if err != nil {
			log.Printf("upgrade failed: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		log.Printf("new session from %s", session.RemoteAddr())
		go handleSession(session, cfg)
	}
}

func handleSession(session *webtransport.Session, cfg serverConfig) {
	for {
		stream, err := session.AcceptStream(context.Background())
		if err != nil {
			log.Printf("session closed: %v", err)
			return
		}
		go func() {
			defer stream.Close()
			reader := bufio.NewReader(stream)
			for {
				msg, err := message.Read(reader)
				if err != nil {
					if err != io.EOF {
						log.Printf("stream read error: %v", err)
					}
					return
				}
				inputBytes := len(msg)
				log.Printf("received: %s (%d bytes)", msg, inputBytes)

				stats, err := llm.StreamChatCompletion(cfg.llmBaseURL, cfg.llmModel, msg, func(token string) error {
					return message.Write(stream, token)
				})
				if err != nil {
					log.Printf("llm error: %v", err)
					message.Write(stream, "\n[error: "+err.Error()+"]")
				}

				log.Printf("stats: input=%d bytes, from_llm=%d bytes, to_client=%d bytes, prompt_tokens=%d, completion_tokens=%d",
					inputBytes, stats.BytesReceived, stats.BytesSent, stats.PromptTokens, stats.CompletionTokens)

				// Send an empty message to signal end of response
				if err := message.Write(stream, ""); err != nil {
					log.Printf("stream write error: %v", err)
					return
				}
			}
		}()
	}
}
