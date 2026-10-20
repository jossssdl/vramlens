package gguf

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Target identifies a model on disk or on the Hugging Face Hub.
type Target struct {
	Display string
	Local   string // path of the first shard, if local
	URL     string // resolve URL of the first shard, if remote
	Size    uint64 // total bytes across all shards
	Shards  int
}

// Candidate is a selectable GGUF in a repo (shards grouped).
type Candidate struct {
	Name   string
	Size   uint64
	Shards int
	first  string
}

// AmbiguousError lists the choices when a repo holds several GGUFs.
type AmbiguousError struct {
	Repo       string
	Candidates []Candidate
}

func (e *AmbiguousError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s contains %d GGUF files; pick one with --quant or --file:", e.Repo, len(e.Candidates))
	for _, c := range e.Candidates {
		fmt.Fprintf(&b, "\n  %-60s %6.2f GiB", c.Name, float64(c.Size)/(1<<30))
	}
	return b.String()
}

var shardRe = regexp.MustCompile(`^(.*)-(\d{5})-of-(\d{5})\.gguf$`)

// ResolveLocal handles a file path, summing sibling shards when present.
func ResolveLocal(path string) (*Target, error) {
	st, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	t := &Target{Display: filepath.Base(path), Local: path, Size: uint64(st.Size()), Shards: 1}
	if m := shardRe.FindStringSubmatch(filepath.Base(path)); m != nil {
		dir := filepath.Dir(path)
		matches, _ := filepath.Glob(filepath.Join(dir, m[1]+"-?????-of-"+m[3]+".gguf"))
		sort.Strings(matches)
		if len(matches) > 0 {
			t.Local, t.Size, t.Shards = matches[0], 0, len(matches)
			for _, f := range matches {
				if s, err := os.Stat(f); err == nil {
					t.Size += uint64(s.Size())
				}
			}
		}
	}
	return t, nil
}

type treeEntry struct {
	Type string `json:"type"`
	Path string `json:"path"`
	Size int64  `json:"size"`
	LFS  *struct {
		Size int64 `json:"size"`
	} `json:"lfs"`
}

// ResolveHF resolves "org/repo" (optionally ":QUANT") or a huggingface.co URL
// to a concrete GGUF. file selects an exact path, quant a filename substring.
func ResolveHF(spec, file, quant, token string) (*Target, error) {
	repo, file, quant := parseHFSpec(spec, file, quant)
	if file != "" && strings.HasPrefix(spec, "https://") && strings.Contains(spec, "/resolve/") {
		// Direct URL to a single file: size is learned from Content-Range later.
		return &Target{Display: file, URL: spec, Shards: 1}, nil
	}
	entries, err := listTree(repo, token)
	if err != nil {
		return nil, err
	}
	groups := map[string]*Candidate{}
	for _, e := range entries {
		if e.Type != "file" || !strings.HasSuffix(strings.ToLower(e.Path), ".gguf") {
			continue
		}
		if strings.Contains(strings.ToLower(filepath.Base(e.Path)), "mmproj") {
			continue
		}
		sz := e.Size
		if e.LFS != nil && e.LFS.Size > 0 {
			sz = e.LFS.Size
		}
		name, first := e.Path, e.Path
		if m := shardRe.FindStringSubmatch(e.Path); m != nil {
			name = m[1] + ".gguf"
			first = ""
		}
		c := groups[name]
		if c == nil {
			c = &Candidate{Name: name}
			groups[name] = c
		}
		c.Size += uint64(sz)
		c.Shards++
		if first != "" {
			c.first = first
		} else if m := shardRe.FindStringSubmatch(e.Path); m != nil && m[2] == "00001" {
			c.first = e.Path
		}
	}
	var cands []Candidate
	for _, c := range groups {
		if file != "" && c.Name != file && c.first != file {
			continue
		}
		if quant != "" && !strings.Contains(strings.ToLower(c.Name), strings.ToLower(quant)) {
			continue
		}
		cands = append(cands, *c)
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].Name < cands[j].Name })
	switch len(cands) {
	case 0:
		return nil, fmt.Errorf("no matching .gguf found in %s (file=%q quant=%q)", repo, file, quant)
	case 1:
		c := cands[0]
		return &Target{
			Display: c.Name,
			URL:     "https://huggingface.co/" + repo + "/resolve/main/" + c.first,
			Size:    c.Size,
			Shards:  c.Shards,
		}, nil
	}
	return nil, &AmbiguousError{Repo: repo, Candidates: cands}
}

func parseHFSpec(spec, file, quant string) (repo, f, q string) {
	f, q = file, quant
	s := strings.TrimPrefix(spec, "https://huggingface.co/")
	s = strings.TrimPrefix(s, "hf.co/")
	if i := strings.Index(s, "/resolve/"); i >= 0 {
		rest := s[i+len("/resolve/"):] // main/path/file.gguf
		if j := strings.Index(rest, "/"); j >= 0 && f == "" {
			f = rest[j+1:]
		}
		s = s[:i]
	}
	if i := strings.Index(s, ":"); i >= 0 {
		if q == "" {
			q = s[i+1:]
		}
		s = s[:i]
	}
	parts := strings.Split(strings.Trim(s, "/"), "/")
	if len(parts) > 2 {
		parts = parts[:2]
	}
	return strings.Join(parts, "/"), f, q
}

func listTree(repo, token string) ([]treeEntry, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	url := "https://huggingface.co/api/models/" + repo + "/tree/main?recursive=true"
	var all []treeEntry
	for url != "" {
		req, err := http.NewRequest(http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != http.StatusOK {
			err := httpError(resp)
			resp.Body.Close()
			return nil, fmt.Errorf("listing %s: %w", repo, err)
		}
		var page []treeEntry
		err = json.NewDecoder(resp.Body).Decode(&page)
		link := resp.Header.Get("Link")
		resp.Body.Close()
		if err != nil {
			return nil, err
		}
		all = append(all, page...)
		url = nextLink(link)
	}
	return all, nil
}

var nextRe = regexp.MustCompile(`<([^>]+)>;\s*rel="next"`)

func nextLink(h string) string {
	if m := nextRe.FindStringSubmatch(h); m != nil {
		return m[1]
	}
	return ""
}
