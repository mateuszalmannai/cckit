// Package capture runs a local reverse proxy in front of the Anthropic API and records the
// /v1/messages request bodies Claude Code sends through it, together with the usage the API
// reports back. Headers are forwarded but never recorded, so no credential reaches disk.
package capture

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
)

// Upstream is where the proxy forwards requests.
const Upstream = "https://api.anthropic.com"

// Usage is the input-token accounting the API reports for one request.
type Usage struct {
	InputTokens              int `json:"input_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
}

// Total is every input token the model saw, cached or not.
func (u Usage) Total() int {
	return u.InputTokens + u.CacheCreationInputTokens + u.CacheReadInputTokens
}

// Exchange is one recorded /v1/messages call.
type Exchange struct {
	Path    string          `json:"path"`
	Request json.RawMessage `json:"request"`
	Usage   *Usage          `json:"usage,omitempty"`
}

// Proxy forwards everything to Upstream and records /v1/messages exchanges.
type Proxy struct {
	mu        sync.Mutex
	exchanges []*Exchange
	listener  net.Listener
	server    *http.Server
	onRecord  func(*Exchange)
}

// Start listens on a free localhost port. onRecord, if set, is called once a response's usage
// is known.
func Start(onRecord func(*Exchange)) (*Proxy, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	up, _ := url.Parse(Upstream)
	p := &Proxy{listener: ln, onRecord: onRecord}
	rp := httputil.NewSingleHostReverseProxy(up)
	baseDirector := rp.Director
	rp.Director = func(r *http.Request) {
		baseDirector(r)
		r.Host = up.Host
		// Plain bodies make the usage parsing below straightforward.
		r.Header.Del("Accept-Encoding")
	}
	rp.FlushInterval = -1 // stream SSE through unbuffered
	rp.ModifyResponse = p.modifyResponse
	p.server = &http.Server{Handler: p.handler(rp)}
	go p.server.Serve(ln)
	return p, nil
}

// URL is the base URL to give Claude Code as ANTHROPIC_BASE_URL.
func (p *Proxy) URL() string { return "http://" + p.listener.Addr().String() }

// Close stops the proxy.
func (p *Proxy) Close() error { return p.server.Close() }

// Exchanges returns the recorded exchanges in arrival order.
func (p *Proxy) Exchanges() []*Exchange {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]*Exchange(nil), p.exchanges...)
}

type ctxKey struct{}

func (p *Proxy) handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/v1/messages") {
			body, err := io.ReadAll(r.Body)
			r.Body.Close()
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadGateway)
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(body))
			ex := &Exchange{Path: r.URL.Path, Request: append(json.RawMessage(nil), body...)}
			p.mu.Lock()
			p.exchanges = append(p.exchanges, ex)
			p.mu.Unlock()
			r = r.WithContext(contextWith(r, ex))
		}
		next.ServeHTTP(w, r)
	})
}

func (p *Proxy) modifyResponse(resp *http.Response) error {
	ex, ok := resp.Request.Context().Value(ctxKey{}).(*Exchange)
	if !ok {
		return nil
	}
	resp.Body = &usageSniffer{rc: resp.Body, sse: strings.Contains(resp.Header.Get("Content-Type"), "event-stream"), done: func(u *Usage) {
		p.mu.Lock()
		ex.Usage = u
		p.mu.Unlock()
		if p.onRecord != nil {
			p.onRecord(ex)
		}
	}}
	return nil
}

// usageSniffer passes the response body through and, on close, extracts the usage block from
// either a JSON body or the SSE message_start event.
type usageSniffer struct {
	rc   io.ReadCloser
	sse  bool
	buf  bytes.Buffer
	done func(*Usage)
	once sync.Once
}

func (s *usageSniffer) Read(b []byte) (int, error) {
	n, err := s.rc.Read(b)
	if n > 0 && s.buf.Len() < 1<<20 {
		s.buf.Write(b[:n])
	}
	if err == io.EOF {
		s.finish()
	}
	return n, err
}

func (s *usageSniffer) Close() error {
	s.finish()
	return s.rc.Close()
}

func (s *usageSniffer) finish() {
	s.once.Do(func() { s.done(parseUsage(s.buf.Bytes(), s.sse)) })
}

func parseUsage(body []byte, sse bool) *Usage {
	if !sse {
		var m struct {
			Usage *Usage `json:"usage"`
		}
		if json.Unmarshal(body, &m) == nil {
			return m.Usage
		}
		return nil
	}
	sc := bufio.NewScanner(bytes.NewReader(body))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line, ok := strings.CutPrefix(sc.Text(), "data: ")
		if !ok {
			continue
		}
		var ev struct {
			Type    string `json:"type"`
			Message struct {
				Usage *Usage `json:"usage"`
			} `json:"message"`
		}
		if json.Unmarshal([]byte(line), &ev) == nil && ev.Type == "message_start" {
			return ev.Message.Usage
		}
	}
	return nil
}
