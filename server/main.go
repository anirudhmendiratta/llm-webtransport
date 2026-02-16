package main

import (
	"crypto/tls"
	"log"
	"net/http"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
	"github.com/quic-go/webtransport-go"
)

func main() {
	tlsCert, err := tls.LoadX509KeyPair("certs/cert.pem", "certs/key.pem")
	if err != nil {
		log.Fatalf("failed to load TLS certificate: %v (run ./generate_cert.sh first)", err)
	}

	h3srv := &http3.Server{
		Addr: ":4433",
		TLSConfig: &tls.Config{
			Certificates: []tls.Certificate{tlsCert},
			NextProtos:   []string{"h3"},
		},
		QUICConfig: &quic.Config{
			MaxIdleTimeout:  5 * time.Minute,
			KeepAlivePeriod: 30 * time.Second,
		},
	}
	webtransport.ConfigureHTTP3Server(h3srv)

	s := webtransport.Server{
		H3:          h3srv,
		CheckOrigin: func(r *http.Request) bool { return true },
	}

	cfg := serverConfig{
		llmBaseURL: "http://127.0.0.1:11434",
		llmModel:   "gemma3:12b",
	}

	http.HandleFunc("/wt", handleHttpToWebTransportUpgrade(&s, cfg))

	log.Println("WebTransport server listening on :4433")
	if err := s.ListenAndServe(); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
