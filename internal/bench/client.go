// Package bench measures latency, throughput and structured-output
// reliability of local LLM servers (Ollama and OpenAI-compatible).
package bench

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Provider selects the wire protocol.
type Provider string

const (
	Ollama Provider = "ollama"
	OpenAI Provider = "openai" // OpenAI-compatible: llama.cpp server, LM Studio, vLLM...
)

// Config describes one benchmark.
type Config struct {
	Provider    Provider
	Endpoint    string // base URL, e.g. http://localhost:11434
	APIKey      string
	Model       string
	Prompt      string
	Schema      map[string]any
	N           int
	Concurrency int
	Temperature float64
	Seed        *int
	NumCtx      int  // Ollama only: context window (options.num_ctx); 0 = server default
	NoThink     bool // Ollama only: disable reasoning ("think": false)
	MaxTokens   int  // cap on generated tokens (num_predict / max_tokens); 0 = server default
	// OnStart, if set, is called from worker goroutines when request i begins;
	// it must be safe for concurrent use.
	OnStart func(index int)
	Timeout time.Duration // per request
}

// Sample is the outcome of a single request.
type Sample struct {
	Index      int           `json:"index"`
	TTFT       time.Duration `json:"ttft_ns"`
	Total      time.Duration `json:"total_ns"`
	Tokens     int           `json:"tokens"`
	TPS        float64       `json:"tps"`
	Output     string        `json:"output"`
	ParseOK    bool          `json:"parse_ok"`  // output is a single valid JSON document
	SchemaOK   bool          `json:"schema_ok"` // ...that also satisfies the schema
	Violations []string      `json:"violations,omitempty"`
	Err        string        `json:"error,omitempty"`
	Truncated  bool          `json:"truncated,omitempty"` // generation stopped at the token limit
}

// stream is what a protocol adapter reports back to the timing logic.
type streamResult struct {
	text      string
	ttft      time.Duration
	tokens    int           // 0 if the server did not report any
	decodeDur time.Duration // server-reported decode time, 0 if unknown
	chunks    int
	truncated bool // the server stopped at the token limit (done_reason/finish_reason = length)
	thought   bool // reasoning tokens were streamed
}

// runOne issues one request and fills timing and validity fields.
func runOne(ctx context.Context, cfg Config, httpc *http.Client, idx int) Sample {
	s := Sample{Index: idx}
	ctx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()

	start := time.Now()
	var (
		res streamResult
		err error
	)
	switch cfg.Provider {
	case OpenAI:
		res, err = streamOpenAI(ctx, cfg, httpc, start)
	default:
		res, err = streamOllama(ctx, cfg, httpc, start)
	}
	s.Total = time.Since(start)
	s.Output = res.text
	if err != nil {
		s.Err = err.Error()
		return s
	}
	s.TTFT = res.ttft
	s.Tokens = res.tokens
	if s.Tokens == 0 {
		s.Tokens = res.chunks // approximation: one token per streamed chunk
	}
	switch {
	case res.decodeDur > 0 && res.tokens > 0:
		s.TPS = float64(res.tokens) / res.decodeDur.Seconds()
	case s.Total > s.TTFT && s.Tokens > 1:
		// First token arrives with the prefill; the remaining ones are decode.
		s.TPS = float64(s.Tokens-1) / (s.Total - s.TTFT).Seconds()
	}
	s.ParseOK, s.SchemaOK, s.Violations = judge(res.text, cfg.Schema)
	s.Truncated = res.truncated
	if !s.ParseOK {
		// Explain the usual causes first; the raw parser error follows.
		switch {
		case strings.TrimSpace(res.text) == "" && res.thought:
			s.Violations = append([]string{"reasoning only, no answer — raise max tokens or disable thinking"}, s.Violations...)
		case res.truncated:
			s.Violations = append([]string{"cut off at the token limit — raise max tokens"}, s.Violations...)
		}
	}
	return s
}

// judge decides structural validity of a model output.
func judge(out string, schema map[string]any) (parseOK, schemaOK bool, violations []string) {
	dec := json.NewDecoder(strings.NewReader(out))
	var v any
	if err := dec.Decode(&v); err != nil {
		return false, false, []string{"invalid JSON: " + err.Error()}
	}
	if _, err := dec.Token(); err != io.EOF {
		return false, false, []string{"trailing content after JSON document"}
	}
	if schema == nil {
		return true, true, nil
	}
	viol := Validate(schema, v)
	return true, len(viol) == 0, viol
}

func post(ctx context.Context, httpc *http.Client, url, key string, body any) (*http.Response, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := httpc.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	return resp, nil
}

func joinURL(base, path string) string {
	return strings.TrimRight(base, "/") + path
}

func streamOllama(ctx context.Context, cfg Config, httpc *http.Client, start time.Time) (streamResult, error) {
	opts := map[string]any{"temperature": cfg.Temperature}
	if cfg.Seed != nil {
		opts["seed"] = *cfg.Seed
	}
	if cfg.MaxTokens > 0 {
		opts["num_predict"] = cfg.MaxTokens
	}
	if cfg.NumCtx > 0 {
		opts["num_ctx"] = cfg.NumCtx
	}
	body := map[string]any{
		"model":    cfg.Model,
		"messages": []map[string]string{{"role": "user", "content": cfg.Prompt}},
		"stream":   true,
		"options":  opts,
	}
	if cfg.NoThink {
		body["think"] = false
	}
	if cfg.Schema != nil {
		body["format"] = cfg.Schema
	} else {
		body["format"] = "json"
	}
	resp, err := post(ctx, httpc, joinURL(cfg.Endpoint, "/api/chat"), cfg.APIKey, body)
	if err != nil {
		return streamResult{}, err
	}
	defer resp.Body.Close()

	var res streamResult
	var sb strings.Builder
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var m struct {
			Message struct {
				Content  string `json:"content"`
				Thinking string `json:"thinking"`
			} `json:"message"`
			Done       bool   `json:"done"`
			DoneReason string `json:"done_reason"`
			Error      string `json:"error"`
			EvalCount  int    `json:"eval_count"`
			EvalDur    int64  `json:"eval_duration"`
		}
		if err := json.Unmarshal(line, &m); err != nil {
			return res, fmt.Errorf("bad stream line: %w", err)
		}
		if m.Error != "" {
			return res, fmt.Errorf("server error: %s", m.Error)
		}
		// Reasoning ("thinking") tokens count for TTFT/throughput but are not
		// part of the answer that gets validated.
		if m.Message.Content != "" || m.Message.Thinking != "" {
			if res.chunks == 0 {
				res.ttft = time.Since(start)
			}
			res.chunks++
			if m.Message.Thinking != "" {
				res.thought = true
			}
			sb.WriteString(m.Message.Content)
		}
		if m.Done {
			res.tokens = m.EvalCount
			res.decodeDur = time.Duration(m.EvalDur)
			res.truncated = m.DoneReason == "length"
		}
	}
	res.text = sb.String()
	if err := sc.Err(); err != nil {
		return res, err
	}
	if res.chunks == 0 {
		return res, fmt.Errorf("empty response")
	}
	return res, nil
}

func streamOpenAI(ctx context.Context, cfg Config, httpc *http.Client, start time.Time) (streamResult, error) {
	body := map[string]any{
		"model":          cfg.Model,
		"messages":       []map[string]string{{"role": "user", "content": cfg.Prompt}},
		"stream":         true,
		"stream_options": map[string]any{"include_usage": true},
		"temperature":    cfg.Temperature,
	}
	if cfg.Seed != nil {
		body["seed"] = *cfg.Seed
	}
	if cfg.MaxTokens > 0 {
		body["max_tokens"] = cfg.MaxTokens
	}
	if cfg.Schema != nil {
		body["response_format"] = map[string]any{
			"type":        "json_schema",
			"json_schema": map[string]any{"name": "response", "strict": true, "schema": cfg.Schema},
		}
	} else {
		body["response_format"] = map[string]any{"type": "json_object"}
	}
	path := "/v1/chat/completions"
	if strings.HasSuffix(strings.TrimRight(cfg.Endpoint, "/"), "/v1") {
		path = "/chat/completions"
	}
	resp, err := post(ctx, httpc, joinURL(cfg.Endpoint, path), cfg.APIKey, body)
	if err != nil {
		return streamResult{}, err
	}
	defer resp.Body.Close()

	var res streamResult
	var sb strings.Builder
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}
		var m struct {
			Choices []struct {
				Delta struct {
					Content          string `json:"content"`
					ReasoningContent string `json:"reasoning_content"`
				} `json:"delta"`
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
			Usage *struct {
				CompletionTokens int `json:"completion_tokens"`
			} `json:"usage"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(data), &m); err != nil {
			return res, fmt.Errorf("bad SSE data: %w", err)
		}
		if m.Error != nil {
			return res, fmt.Errorf("server error: %s", m.Error.Message)
		}
		if m.Usage != nil {
			res.tokens = m.Usage.CompletionTokens
		}
		for _, c := range m.Choices {
			if c.FinishReason == "length" {
				res.truncated = true
			}
			if c.Delta.Content != "" || c.Delta.ReasoningContent != "" {
				if res.chunks == 0 {
					res.ttft = time.Since(start)
				}
				res.chunks++
				if c.Delta.ReasoningContent != "" {
					res.thought = true
				}
				sb.WriteString(c.Delta.Content)
			}
		}
	}
	res.text = sb.String()
	if err := sc.Err(); err != nil {
		return res, err
	}
	if res.chunks == 0 {
		return res, fmt.Errorf("empty response")
	}
	return res, nil
}

// ListModels asks the server which models it offers (Ollama /api/tags,
// OpenAI-compatible /v1/models). It is a convenience for interactive use.
func ListModels(ctx context.Context, p Provider, endpoint, apiKey string) ([]string, error) {
	path := "/api/tags"
	if p == OpenAI {
		path = "/v1/models"
		if strings.HasSuffix(strings.TrimRight(endpoint, "/"), "/v1") {
			path = "/models"
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, joinURL(endpoint, path), nil)
	if err != nil {
		return nil, err
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	var body struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	var out []string
	for _, m := range body.Models {
		out = append(out, m.Name)
	}
	for _, m := range body.Data {
		out = append(out, m.ID)
	}
	return out, nil
}
