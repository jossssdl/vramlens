package gguf

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// Source is random-access, read-only storage with a known total size.
type Source interface {
	io.ReaderAt
	Size() int64
	Close() error
}

type localSource struct {
	f    *os.File
	size int64
}

// OpenLocal opens a GGUF file on disk.
func OpenLocal(path string) (Source, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	return &localSource{f: f, size: st.Size()}, nil
}

func (s *localSource) ReadAt(p []byte, off int64) (int, error) { return s.f.ReadAt(p, off) }
func (s *localSource) Size() int64                             { return s.size }
func (s *localSource) Close() error                            { return s.f.Close() }

type httpSource struct {
	url     string
	token   string
	client  *http.Client
	size    int64
	fetched int64 // bytes transferred, for diagnostics and tests
}

// OpenHTTP prepares a remote GGUF; it issues a 1-byte Range request to learn
// the total size from Content-Range.
func OpenHTTP(url, token string) (Source, error) {
	s := &httpSource{url: url, token: token, client: &http.Client{Timeout: 60 * time.Second}}
	resp, err := s.do(0, 0)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	switch resp.StatusCode {
	case http.StatusPartialContent:
		cr := resp.Header.Get("Content-Range") // bytes 0-0/12345
		i := strings.LastIndex(cr, "/")
		if i < 0 {
			return nil, fmt.Errorf("bad Content-Range %q", cr)
		}
		s.size, err = strconv.ParseInt(cr[i+1:], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("bad Content-Range %q", cr)
		}
	case http.StatusOK:
		return nil, fmt.Errorf("server ignored Range request for %s", url)
	default:
		return nil, httpError(resp)
	}
	return s, nil
}

func httpError(resp *http.Response) error {
	switch resp.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Errorf("HTTP %d: access denied (gated/private repo? set HF_TOKEN)", resp.StatusCode)
	case http.StatusNotFound:
		return fmt.Errorf("HTTP 404: not found")
	}
	return fmt.Errorf("HTTP %s", resp.Status)
}

func (s *httpSource) do(start, end int64) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodGet, s.url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", start, end))
	if s.token != "" {
		req.Header.Set("Authorization", "Bearer "+s.token)
	}
	return s.client.Do(req)
}

func (s *httpSource) ReadAt(p []byte, off int64) (int, error) {
	if off >= s.size {
		return 0, io.EOF
	}
	want := int64(len(p))
	eof := false
	if off+want > s.size {
		want = s.size - off
		eof = true
	}
	resp, err := s.do(off, off+want-1)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent {
		return 0, httpError(resp)
	}
	n, err := io.ReadFull(resp.Body, p[:want])
	s.fetched += int64(n)
	if err != nil {
		return n, err
	}
	if eof {
		return n, io.EOF
	}
	return n, nil
}

func (s *httpSource) Size() int64  { return s.size }
func (s *httpSource) Close() error { return nil }

// Fetched reports bytes downloaded by a Source created with OpenHTTP.
func Fetched(s Source) int64 {
	if h, ok := s.(*httpSource); ok {
		return h.fetched
	}
	return 0
}
