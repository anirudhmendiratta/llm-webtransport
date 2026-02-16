package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"slices"
	"strings"
	"time"

	"llm-webtransport/message"

	"github.com/quic-go/quic-go"
	"github.com/quic-go/webtransport-go"
)

var reuseConn = flag.Bool("reuse", false, "Reuse connections across prompts (simulates persistent browser connection)")

// CountingReader wraps an io.Reader and counts bytes read through it.
type CountingReader struct {
	r     io.Reader
	Count int64
}

func (cr *CountingReader) Read(p []byte) (int, error) {
	n, err := cr.r.Read(p)
	cr.Count += int64(n)
	return n, err
}

// Result holds metrics from a single benchmark run.
type Result struct {
	BytesReceived       int64
	TTFT                time.Duration
	TokenCount          int
	TotalInterTokenTime time.Duration
	TotalTime           time.Duration
}

// Runner is the interface each streaming approach implements.
type Runner interface {
	Name() string
	Run(prompt string) (Result, error)
	Close() error
}

// --- LLM request/response types (local copies) ---

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
}

// --- HTTP SSE request type ---

type httpChatRequest struct {
	Message string `json:"message"`
}

// --- Prompts ---

var prompts = []string{
	"What is the capital of France?",
	"Explain the difference between a stack and a queue.",
	"Write a haiku about programming.",
	"What are the first 10 prime numbers?",
	"Explain how a hash table works in simple terms.",
	"Write a short Go function that reverses a string.",
	"What causes a rainbow to appear?",
	"Compare TCP and UDP in three sentences.",
	"What is the time complexity of binary search and why?",
	"Describe the observer design pattern briefly.",
}

// startTLSProxy starts a TLS reverse proxy to Ollama so the Raw API runner
// pays the same TCP+TLS handshake cost as other approaches.
func startTLSProxy(ollamaAddr string) (string, error) {
	target, err := url.Parse("http://" + ollamaAddr)
	if err != nil {
		return "", err
	}
	proxy := httputil.NewSingleHostReverseProxy(target)

	tlsCert, err := tls.LoadX509KeyPair("certs/cert.pem", "certs/key.pem")
	if err != nil {
		return "", fmt.Errorf("load TLS cert: %w", err)
	}

	ln, err := tls.Listen("tcp", "127.0.0.1:11435", &tls.Config{
		Certificates: []tls.Certificate{tlsCert},
	})
	if err != nil {
		return "", err
	}

	go http.Serve(ln, proxy)
	return ln.Addr().(*net.TCPAddr).String(), nil
}

// =============================================
// rawAPIRunner — direct Ollama API (via TLS proxy)
// =============================================

type rawAPIRunner struct {
	proxyAddr string
	client    *http.Client // non-nil when reusing connections
}

func (r *rawAPIRunner) Name() string { return "Raw API" }
func (r *rawAPIRunner) Close() error { return nil }

func (r *rawAPIRunner) Run(prompt string) (Result, error) {
	body, err := json.Marshal(chatRequest{
		Model:    "gemma3:12b",
		Messages: []chatMessage{{Role: "user", Content: prompt}},
		Stream:   true,
	})
	if err != nil {
		return Result{}, err
	}

	client := r.client
	if client == nil {
		// Fresh TCP+TLS connection per prompt.
		client = &http.Client{
			Transport: &http.Transport{
				DisableKeepAlives: true,
				TLSClientConfig:  &tls.Config{InsecureSkipVerify: true},
			},
		}
	}
	start := time.Now()
	resp, err := client.Post("https://"+r.proxyAddr+"/v1/chat/completions", "application/json", bytes.NewReader(body))
	if err != nil {
		return Result{}, err
	}
	defer resp.Body.Close()

	cr := &CountingReader{r: resp.Body}
	scanner := bufio.NewScanner(cr)
	var res Result
	var lastToken time.Time

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}
		var chunk chatChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}
		if len(chunk.Choices) == 0 || chunk.Choices[0].Delta.Content == "" {
			continue
		}
		now := time.Now()
		if res.TokenCount == 0 {
			res.TTFT = now.Sub(start)
		} else {
			res.TotalInterTokenTime += now.Sub(lastToken)
		}
		lastToken = now
		res.TokenCount++
	}
	res.BytesReceived = cr.Count
	res.TotalTime = time.Since(start)
	return res, scanner.Err()
}

// =============================================
// httpSSERunner — our HTTP SSE server
// =============================================

type httpSSERunner struct {
	client *http.Client // non-nil when reusing connections
}

func (r *httpSSERunner) Name() string { return "HTTP SSE" }
func (r *httpSSERunner) Close() error { return nil }

func (r *httpSSERunner) Run(prompt string) (Result, error) {
	body, err := json.Marshal(httpChatRequest{Message: prompt})
	if err != nil {
		return Result{}, err
	}

	client := r.client
	if client == nil {
		// Fresh TCP+TLS connection per prompt.
		client = &http.Client{
			Transport: &http.Transport{
				DisableKeepAlives: true,
				TLSClientConfig:  &tls.Config{InsecureSkipVerify: true},
			},
		}
	}
	start := time.Now()
	resp, err := client.Post("https://localhost:8080/chat", "application/json", bytes.NewReader(body))
	if err != nil {
		return Result{}, err
	}
	defer resp.Body.Close()

	cr := &CountingReader{r: resp.Body}
	scanner := bufio.NewScanner(cr)
	var res Result
	var lastToken time.Time

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}
		now := time.Now()
		if res.TokenCount == 0 {
			res.TTFT = now.Sub(start)
		} else {
			res.TotalInterTokenTime += now.Sub(lastToken)
		}
		lastToken = now
		res.TokenCount++
	}
	res.BytesReceived = cr.Count
	res.TotalTime = time.Since(start)
	return res, scanner.Err()
}

// =============================================
// webtransportRunner — WebTransport over QUIC
// =============================================

type webtransportRunner struct {
	sess *webtransport.Session // non-nil when reusing connections
}

func newWebtransportRunner(reuse bool) (*webtransportRunner, error) {
	d := webtransport.Dialer{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		QUICConfig: &quic.Config{
			EnableDatagrams:                  true,
			EnableStreamResetPartialDelivery: true,
		},
	}
	_, sess, err := d.Dial(context.Background(), "https://localhost:4433/wt", nil)
	if err != nil {
		return nil, fmt.Errorf("webtransport dial: %w", err)
	}
	if reuse {
		return &webtransportRunner{sess: sess}, nil
	}
	sess.CloseWithError(0, "connectivity check")
	return &webtransportRunner{}, nil
}

func (r *webtransportRunner) Name() string { return "WebTransport" }

func (r *webtransportRunner) Close() error {
	if r.sess != nil {
		return r.sess.CloseWithError(0, "benchmark done")
	}
	return nil
}

func (r *webtransportRunner) Run(prompt string) (Result, error) {
	start := time.Now()
	sess := r.sess
	if sess == nil {
		// Fresh QUIC session per prompt to capture connection setup cost.
		d := webtransport.Dialer{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			QUICConfig: &quic.Config{
				EnableDatagrams:                  true,
				EnableStreamResetPartialDelivery: true,
			},
		}
		var err error
		_, sess, err = d.Dial(context.Background(), "https://localhost:4433/wt", nil)
		if err != nil {
			return Result{}, fmt.Errorf("webtransport dial: %w", err)
		}
		defer sess.CloseWithError(0, "prompt done")
	}

	stream, err := sess.OpenStream()
	if err != nil {
		return Result{}, fmt.Errorf("open stream: %w", err)
	}
	if err := message.Write(stream, prompt); err != nil {
		return Result{}, fmt.Errorf("write prompt: %w", err)
	}
	// Close write side so server knows the prompt is complete.
	if err := stream.Close(); err != nil {
		return Result{}, fmt.Errorf("close write: %w", err)
	}

	cr := &CountingReader{r: stream}
	reader := bufio.NewReader(cr)
	var res Result
	var lastToken time.Time

	for {
		_, err := message.Read(reader)
		if err == io.EOF {
			break
		}
		if err != nil {
			return Result{}, fmt.Errorf("read token: %w", err)
		}
		now := time.Now()
		if res.TokenCount == 0 {
			res.TTFT = now.Sub(start)
		} else {
			res.TotalInterTokenTime += now.Sub(lastToken)
		}
		lastToken = now
		res.TokenCount++
	}
	res.BytesReceived = cr.Count
	res.TotalTime = time.Since(start)
	return res, nil
}

// percentile returns the p-th percentile from a sorted slice using nearest-rank.
func percentile(sorted []int64, p float64) int64 {
	if len(sorted) == 0 {
		return 0
	}
	rank := int(math.Ceil(p/100*float64(len(sorted)))) - 1
	if rank < 0 {
		rank = 0
	}
	return sorted[rank]
}

// =============================================
// Main
// =============================================

func main() {
	flag.Parse()

	proxyAddr, err := startTLSProxy("127.0.0.1:11434")
	if err != nil {
		fmt.Printf("Fatal: could not start TLS proxy: %v\n", err)
		return
	}
	fmt.Printf("TLS proxy to Ollama listening on %s\n", proxyAddr)

	if *reuseConn {
		fmt.Println("Mode: connection reuse (persistent connections)")
	} else {
		fmt.Println("Mode: fresh connection per prompt")
	}

	// Warmup: send a short request to Ollama so the model is loaded before benchmarking.
	fmt.Print("Warming up Ollama model... ")
	warmupBody, _ := json.Marshal(chatRequest{
		Model:    "gemma3:12b",
		Messages: []chatMessage{{Role: "user", Content: "hi"}},
		Stream:   false,
	})
	warmupResp, err := http.Post("http://127.0.0.1:11434/v1/chat/completions", "application/json", bytes.NewReader(warmupBody))
	if err != nil {
		fmt.Printf("warning: warmup failed: %v\n", err)
	} else {
		io.Copy(io.Discard, warmupResp.Body)
		warmupResp.Body.Close()
		fmt.Println("done")
	}

	wtRunner, err := newWebtransportRunner(*reuseConn)
	if err != nil {
		fmt.Printf("Warning: WebTransport unavailable: %v\n", err)
	}

	rawRunner := &rawAPIRunner{proxyAddr: proxyAddr}
	sseRunner := &httpSSERunner{}
	if *reuseConn {
		tlsTransport := &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		}
		rawRunner.client = &http.Client{Transport: tlsTransport}
		sseRunner.client = &http.Client{Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		}}
	}

	runners := []Runner{rawRunner, sseRunner}
	if wtRunner != nil {
		runners = append(runners, wtRunner)
	}

	type stats struct {
		totalBytes       int64
		totalTTFT        time.Duration
		totalInterToken  time.Duration
		totalTime        time.Duration
		totalTokens      int
		promptsCompleted int
		bytesSamples     []int64
	}

	results := make(map[string]*stats)
	for _, r := range runners {
		results[r.Name()] = &stats{}
	}

	for _, runner := range runners {
		fmt.Printf("\n=== %s ===\n", runner.Name())
		for i, prompt := range prompts {
			fmt.Printf("  [%d/%d] %s... ", i+1, len(prompts), prompt[:min(40, len(prompt))])
			res, err := runner.Run(prompt)
			if err != nil {
				fmt.Printf("ERROR: %v\n", err)
				continue
			}
			s := results[runner.Name()]
			s.bytesSamples = append(s.bytesSamples, res.BytesReceived)
			s.totalBytes += res.BytesReceived
			s.totalTTFT += res.TTFT
			s.totalInterToken += res.TotalInterTokenTime
			s.totalTime += res.TotalTime
			s.totalTokens += res.TokenCount
			s.promptsCompleted++

			avgTBT := time.Duration(0)
			if res.TokenCount > 1 {
				avgTBT = res.TotalInterTokenTime / time.Duration(res.TokenCount-1)
			}
			bytesPerToken := float64(0)
			if res.TokenCount > 0 {
				bytesPerToken = float64(res.BytesReceived) / float64(res.TokenCount)
			}
			fmt.Printf("%d tokens, TTFT %v, avg TBT %v, %v total, %d bytes, %.1f B/tok\n",
				res.TokenCount, res.TTFT.Round(time.Millisecond), avgTBT.Round(time.Millisecond), res.TotalTime.Round(time.Millisecond), res.BytesReceived, bytesPerToken)
		}
		runner.Close()
	}

	// Print summary table
	fmt.Printf("\n%-15s | %10s | %10s | %10s | %10s | %10s | %10s | %10s | %10s | %10s\n",
		"Approach", "Avg Bytes", "P50 Bytes", "P90 Bytes", "Max Bytes", "Avg TTFT", "Avg TBT", "Avg Total", "Avg Tokens", "Avg B/tok")
	fmt.Println(strings.Repeat("-", 128))
	for _, runner := range runners {
		s := results[runner.Name()]
		if s.promptsCompleted == 0 {
			fmt.Printf("%-15s | %10s | %10s | %10s | %10s | %10s | %10s | %10s | %10s | %10s\n",
				runner.Name(), "N/A", "N/A", "N/A", "N/A", "N/A", "N/A", "N/A", "N/A", "N/A")
			continue
		}
		n := s.promptsCompleted
		avgBytes := s.totalBytes / int64(n)
		avgTTFT := (s.totalTTFT / time.Duration(n)).Round(time.Millisecond)
		avgTBT := time.Duration(0)
		if s.totalTokens > n {
			avgTBT = (s.totalInterToken / time.Duration(s.totalTokens-n)).Round(time.Millisecond)
		}
		avgTotal := (s.totalTime / time.Duration(n)).Round(time.Millisecond)
		avgTokens := s.totalTokens / n

		sorted := slices.Clone(s.bytesSamples)
		slices.Sort(sorted)
		p50 := percentile(sorted, 50)
		p90 := percentile(sorted, 90)
		maxBytes := sorted[len(sorted)-1]

		avgBytesPerToken := float64(0)
		if s.totalTokens > 0 {
			avgBytesPerToken = float64(s.totalBytes) / float64(s.totalTokens)
		}
		fmt.Printf("%-15s | %10d | %10d | %10d | %10d | %10v | %10v | %10v | %10d | %10.1f\n",
			runner.Name(), avgBytes, p50, p90, maxBytes, avgTTFT, avgTBT, avgTotal, avgTokens, avgBytesPerToken)
	}
}
