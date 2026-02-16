package main

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"log"
	"os"
	"time"

	"llm-webtransport/message"

	"github.com/quic-go/quic-go"
	"github.com/quic-go/webtransport-go"
)

func main() {
	d := webtransport.Dialer{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
		},
		QUICConfig: &quic.Config{
			MaxIdleTimeout:                   5 * time.Minute,
			KeepAlivePeriod:                  30 * time.Second,
			EnableDatagrams:                  true,
			EnableStreamResetPartialDelivery: true,
		},
	}

	ctx := context.Background()
	_, session, err := d.Dial(ctx, "https://localhost:4433/wt", nil)
	if err != nil {
		log.Fatalf("dial failed: %v", err)
	}
	defer session.CloseWithError(0, "client closed")

	stream, err := session.OpenStreamSync(ctx)
	if err != nil {
		log.Fatalf("open stream failed: %v", err)
	}
	defer stream.Close()

	reader := bufio.NewReader(stream)
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

		if err := message.Write(stream, text); err != nil {
			log.Fatalf("send failed: %v", err)
		}

		sendTime := time.Now()
		var ttft time.Duration
		tokenCount := 0
		var lastTokenTime time.Time

		var totalInterTokenTime time.Duration

		for {
			token, err := message.Read(reader)
			if err != nil {
				log.Fatalf("receive failed: %v", err)
			}
			if token == "" {
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
			fmt.Print(token)
		}
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
