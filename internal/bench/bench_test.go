package bench

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

var personSchema = mustSchema(`{
  "type": "object",
  "properties": {
    "name": {"type": "string", "minLength": 1},
    "age": {"type": "integer", "minimum": 0},
    "tags": {"type": "array", "items": {"type": "string"}, "maxItems": 2},
    "kind": {"enum": ["a", "b"]}
  },
  "required": ["name", "age"],
  "additionalProperties": false
}`)

func mustSchema(s string) map[string]any {
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		panic(err)
	}
	return m
}

func TestValidate(t *testing.T) {
	cases := []struct {
		name string
		doc  string
		ok   bool
	}{
		{"valid", `{"name":"Ann","age":30,"tags":["x"],"kind":"a"}`, true},
		{"missing required", `{"name":"Ann"}`, false},
		{"wrong type", `{"name":"Ann","age":"30"}`, false},
		{"float for integer", `{"name":"Ann","age":3.5}`, false},
		{"below minimum", `{"name":"Ann","age":-1}`, false},
		{"extra property", `{"name":"Ann","age":1,"x":1}`, false},
		{"too many items", `{"name":"A","age":1,"tags":["a","b","c"]}`, false},
		{"bad enum", `{"name":"A","age":1,"kind":"z"}`, false},
		{"empty name", `{"name":"","age":1}`, false},
	}
	for _, c := range cases {
		var v any
		if err := json.Unmarshal([]byte(c.doc), &v); err != nil {
			t.Fatal(err)
		}
		if got := len(Validate(personSchema, v)) == 0; got != c.ok {
			t.Errorf("%s: valid=%v want %v", c.name, got, c.ok)
		}
	}
}

func TestValidateRefAndCombinators(t *testing.T) {
	s := mustSchema(`{"$defs":{"n":{"type":"number"}},
	  "type":"object","properties":{"v":{"$ref":"#/$defs/n"},"w":{"anyOf":[{"type":"string"},{"type":"null"}]}}}`)
	ok := map[string]any{"v": 1.5, "w": nil}
	if e := Validate(s, ok); len(e) != 0 {
		t.Errorf("unexpected: %v", e)
	}
	if e := Validate(s, map[string]any{"v": "x", "w": 3.0}); len(e) != 2 {
		t.Errorf("want 2 violations, got %v", e)
	}
}

func TestJudgeTrailingContent(t *testing.T) {
	if p, _, _ := judge(`{"name":"a","age":1} extra`, personSchema); p {
		t.Error("trailing text must not parse")
	}
	if p, s, _ := judge("```json\n{}\n```", personSchema); p || s {
		t.Error("markdown fenced JSON must be invalid")
	}
}

func ollamaServer(t *testing.T, outputs func(i int32) string) *httptest.Server {
	var n int32
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			http.NotFound(w, r)
			return
		}
		var req map[string]any
		json.NewDecoder(r.Body).Decode(&req)
		if _, ok := req["format"].(map[string]any); !ok {
			t.Errorf("schema not sent as format: %v", req["format"])
		}
		out := outputs(atomic.AddInt32(&n, 1) - 1)
		fl := w.(http.Flusher)
		mid := len(out) / 2
		for _, part := range []string{out[:mid], out[mid:]} {
			time.Sleep(5 * time.Millisecond)
			b, _ := json.Marshal(map[string]any{"message": map[string]string{"content": part}, "done": false})
			fmt.Fprintf(w, "%s\n", b)
			fl.Flush()
		}
		fmt.Fprintf(w, `{"done":true,"eval_count":40,"eval_duration":2000000000}`+"\n")
	}))
}

func TestRunOllama(t *testing.T) {
	good := `{"name":"Ann","age":30}`
	srv := ollamaServer(t, func(i int32) string {
		if i == 0 {
			return `{"name":"Ann",` // truncated -> invalid
		}
		return good
	})
	defer srv.Close()
	var seen int32
	samples, err := Run(context.Background(), Config{
		Provider: Ollama, Endpoint: srv.URL, Model: "m", Prompt: "p",
		Schema: personSchema, N: 4, Concurrency: 2,
	}, func(Sample) { atomic.AddInt32(&seen, 1) })
	if err != nil {
		t.Fatal(err)
	}
	if len(samples) != 4 || seen != 4 {
		t.Fatalf("samples=%d callbacks=%d", len(samples), seen)
	}
	st := Summarize(samples)
	if st.SchemaRate != 75 || st.ParseRate != 75 {
		t.Errorf("validity: %+v", st)
	}
	if st.TPSMean < 19 || st.TPSMean > 21 { // 40 tokens / 2 s
		t.Errorf("tps %v want 20", st.TPSMean)
	}
	if st.TTFTMean <= 0 {
		t.Errorf("ttft %v", st.TTFTMean)
	}
	if st.Distinct != 2 || st.Determinism != 75 {
		t.Errorf("determinism: %+v", st)
	}
}

func TestRunOpenAI(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer k" {
			http.Error(w, "unauthorized", 401)
			return
		}
		var req struct {
			ResponseFormat struct {
				Type string `json:"type"`
			} `json:"response_format"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		if req.ResponseFormat.Type != "json_schema" {
			t.Errorf("response_format %q", req.ResponseFormat.Type)
		}
		fl := w.(http.Flusher)
		for _, c := range []string{`{"name":`, `"Bo",`, `"age":7}`} {
			fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":%q}}]}\n\n", c)
			fl.Flush()
			time.Sleep(3 * time.Millisecond)
		}
		fmt.Fprint(w, "data: {\"choices\":[],\"usage\":{\"completion_tokens\":12}}\n\ndata: [DONE]\n\n")
	}))
	defer srv.Close()
	samples, err := Run(context.Background(), Config{
		Provider: OpenAI, Endpoint: srv.URL, APIKey: "k", Model: "m", Prompt: "p",
		Schema: personSchema, N: 2, Concurrency: 1,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range samples {
		if s.Err != "" || !s.SchemaOK || s.Tokens != 12 || s.TPS <= 0 {
			t.Errorf("bad sample: %+v", s)
		}
	}
	if st := Summarize(samples); st.Determinism != 100 {
		t.Errorf("determinism %v", st.Determinism)
	}
}

func TestRunServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "model not found", 404)
	}))
	defer srv.Close()
	samples, _ := Run(context.Background(), Config{Provider: Ollama, Endpoint: srv.URL, Model: "m", N: 2}, nil)
	st := Summarize(samples)
	if st.Errors != 2 || st.SchemaRate != 0 {
		t.Errorf("%+v", st)
	}
	if !strings.Contains(samples[0].Err, "404") {
		t.Errorf("err %q", samples[0].Err)
	}
}

func TestPercentile(t *testing.T) {
	v := []float64{10, 20, 30, 40, 50}
	if p := percentile(v, 50); p != 30 {
		t.Errorf("p50 %v", p)
	}
	if p := percentile(v, 95); p < 47 || p > 49 {
		t.Errorf("p95 %v", p)
	}
}

func TestThinkingCountsForTTFTNotOutput(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fl := w.(http.Flusher)
		fmt.Fprintln(w, `{"message":{"thinking":"hmm"},"done":false}`)
		fl.Flush()
		time.Sleep(120 * time.Millisecond)
		fmt.Fprintln(w, `{"message":{"content":"{\"name\":\"Ann\",\"age\":1}"},"done":false}`)
		fmt.Fprintln(w, `{"done":true,"eval_count":10,"eval_duration":1000000000}`)
	}))
	defer srv.Close()
	samples, _ := Run(context.Background(), Config{Provider: Ollama, Endpoint: srv.URL, Model: "m", Schema: personSchema, N: 1}, nil)
	s := samples[0]
	if s.Err != "" || !s.SchemaOK {
		t.Fatalf("thinking must not pollute output: %+v", s)
	}
	if s.TTFT >= 100*time.Millisecond {
		t.Errorf("TTFT %v should be measured at the first (thinking) token", s.TTFT)
	}
}

func TestListModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			fmt.Fprint(w, `{"models":[{"name":"a:1"},{"name":"b:2"}]}`)
		case "/v1/models":
			fmt.Fprint(w, `{"data":[{"id":"x"}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	got, err := ListModels(context.Background(), Ollama, srv.URL, "")
	if err != nil || len(got) != 2 || got[1] != "b:2" {
		t.Errorf("ollama: %v %v", got, err)
	}
	got, err = ListModels(context.Background(), OpenAI, srv.URL, "")
	if err != nil || len(got) != 1 || got[0] != "x" {
		t.Errorf("openai: %v %v", got, err)
	}
}
