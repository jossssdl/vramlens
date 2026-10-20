package bench

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

// Run executes cfg.N requests with cfg.Concurrency workers. onSample (may be
// nil) is invoked serially as each request finishes. Samples are returned
// ordered by index; cancelling ctx stops dispatching new requests.
func Run(ctx context.Context, cfg Config, onSample func(Sample)) ([]Sample, error) {
	if cfg.N <= 0 {
		return nil, errors.New("N must be positive")
	}
	if cfg.Model == "" {
		return nil, errors.New("model is required")
	}
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = 1
	}
	if cfg.Concurrency > cfg.N {
		cfg.Concurrency = cfg.N
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 5 * time.Minute
	}
	httpc := &http.Client{Transport: &http.Transport{MaxIdleConnsPerHost: cfg.Concurrency}}

	jobs := make(chan int)
	out := make([]Sample, cfg.N)
	done := make([]bool, cfg.N)
	var mu sync.Mutex
	var wg sync.WaitGroup
	for w := 0; w < cfg.Concurrency; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				s := runOne(ctx, cfg, httpc, i)
				mu.Lock()
				out[i], done[i] = s, true
				if onSample != nil {
					onSample(s)
				}
				mu.Unlock()
			}
		}()
	}
feed:
	for i := 0; i < cfg.N; i++ {
		select {
		case jobs <- i:
		case <-ctx.Done():
			break feed
		}
	}
	close(jobs)
	wg.Wait()

	res := make([]Sample, 0, cfg.N)
	for i, d := range done {
		if d {
			res = append(res, out[i])
		}
	}
	return res, ctx.Err()
}

// Stats aggregates a set of samples.
type Stats struct {
	Runs        int     `json:"runs"`
	Errors      int     `json:"errors"`
	TTFTMean    float64 `json:"ttft_mean_ms"`
	TTFTP50     float64 `json:"ttft_p50_ms"`
	TTFTP95     float64 `json:"ttft_p95_ms"`
	TPSMean     float64 `json:"tps_mean"`
	TPSMin      float64 `json:"tps_min"`
	TPSMax      float64 `json:"tps_max"`
	ParseRate   float64 `json:"json_parse_pct"`   // of all runs
	SchemaRate  float64 `json:"schema_valid_pct"` // of all runs
	Distinct    int     `json:"distinct_outputs"`
	Determinism float64 `json:"determinism_pct"` // share of the most frequent output, among completed runs
}

// Summarize computes statistics. Failed requests count against validity but
// are excluded from latency/throughput figures.
func Summarize(samples []Sample) Stats {
	st := Stats{Runs: len(samples)}
	var ttft, tps []float64
	counts := map[string]int{}
	completed := 0
	for _, s := range samples {
		if s.Err != "" {
			st.Errors++
			continue
		}
		completed++
		ttft = append(ttft, float64(s.TTFT)/float64(time.Millisecond))
		if s.TPS > 0 {
			tps = append(tps, s.TPS)
		}
		if s.ParseOK {
			st.ParseRate++
		}
		if s.SchemaOK {
			st.SchemaRate++
		}
		counts[canonical(s.Output)]++
	}
	if st.Runs > 0 {
		st.ParseRate = 100 * st.ParseRate / float64(st.Runs)
		st.SchemaRate = 100 * st.SchemaRate / float64(st.Runs)
	}
	st.TTFTMean, st.TTFTP50, st.TTFTP95 = mean(ttft), percentile(ttft, 50), percentile(ttft, 95)
	st.TPSMean = mean(tps)
	if len(tps) > 0 {
		st.TPSMin, st.TPSMax = tps[0], tps[0]
		for _, v := range tps {
			st.TPSMin, st.TPSMax = math.Min(st.TPSMin, v), math.Max(st.TPSMax, v)
		}
	}
	st.Distinct = len(counts)
	top := 0
	for _, c := range counts {
		if c > top {
			top = c
		}
	}
	if completed > 0 {
		st.Determinism = 100 * float64(top) / float64(completed)
	}
	return st
}

// canonical normalizes JSON (key order, whitespace) so formatting-only
// differences are not counted as non-determinism; invalid JSON is kept as-is.
func canonical(out string) string {
	var v any
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		return strings.TrimSpace(out)
	}
	b, _ := json.Marshal(v)
	return string(b)
}

func mean(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	var s float64
	for _, x := range v {
		s += x
	}
	return s / float64(len(v))
}

// percentile uses linear interpolation between closest ranks.
func percentile(v []float64, p float64) float64 {
	if len(v) == 0 {
		return 0
	}
	c := append([]float64(nil), v...)
	sort.Float64s(c)
	if len(c) == 1 {
		return c[0]
	}
	pos := p / 100 * float64(len(c)-1)
	lo := int(math.Floor(pos))
	hi := int(math.Ceil(pos))
	return c[lo] + (c[hi]-c[lo])*(pos-float64(lo))
}
