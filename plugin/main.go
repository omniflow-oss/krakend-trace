// krakend_trace_plugin.go
// Build: CGO_ENABLED=0 go build -trimpath -buildmode=plugin -o krakend-trace-plugin.so
package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

/* ────────── defaults ────────── */

const (
	defTimeoutMS    = 2_000
	defMaxCaptureKB = 256
	headerReqID     = "X-Request-Id"
	tag             = "[krakend-trace-plugin]"
)

/* ────────── cfg & pools ────────── */

type cfg struct {
	url        *url.URL
	timeout    time.Duration
	maxCapture int
	verbose    bool
}

var bufPool   = sync.Pool{New: func() any { return &bytes.Buffer{} }}
var slicePool = sync.Pool{New: func() any { return make([]byte, 0, defMaxCaptureKB*1024) }}

/* ────────── plugin symbols ────────── */

var (
	ClientRegisterer = registerer("krakend-trace-plugin")
	logger           Logger
)

type registerer string

/* ────────── tiny logger helpers ────────── */

func vdbg(c *cfg, v ...interface{}) {
	if c.verbose && logger != nil { logger.Debug(append([]interface{}{tag}, v...)...) }
}
func always(v ...interface{}) { if logger != nil { logger.Debug(append([]interface{}{tag}, v...)...) } }

/* ────────── KrakenD hooks ────────── */

func (registerer) RegisterLogger(v interface{}) {
	if l, ok := v.(Logger); ok { logger = l; always("logger injected") }
}

func (r registerer) RegisterClients(reg func(
	string,
	func(context.Context, map[string]interface{}) (http.Handler, error)),
) {
	reg(string(r), r.registerClients)
}

/* ────────── registerClients ────────── */

func (r registerer) registerClients(_ context.Context, extra map[string]interface{}) (http.Handler, error) {
	sec := extra[string(r)].(map[string]interface{})

	u, err := url.ParseRequestURI(sec["tracking_url"].(string))
	if err != nil { return nil, fmt.Errorf("%s invalid tracking_url: %w", tag, err) }

	c := &cfg{
		url:        u,
		timeout:    defTimeoutMS * time.Millisecond,
		maxCapture: defMaxCaptureKB * 1024,
		verbose:    false,
	}
	if v, ok := sec["timeout_ms"].(float64); ok && v > 0 { c.timeout = time.Duration(v) * time.Millisecond }
	if v, ok := sec["max_capture_kb"].(float64); ok && v > 0 { c.maxCapture = int(v * 1024) }
	if v, ok := sec["verbose"].(bool); ok { c.verbose = v }

	logger.Info(tag, "config →", c.url, "timeout:", c.timeout, "max_cap:", c.maxCapture, "verbose:", c.verbose)

	/* ─── proxy handler ────────────────────────────────────────── */
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		start := time.Now()

		// capture request body up front
		reqBody := captureBody(&req.Body, c.maxCapture)
		vdbg(c, "reqB:", len(reqBody))

		// channel will receive captured response bytes
		respChan := make(chan []byte, 1)

		// detached goroutine – builds payload & POSTs (no block)
		go trackCoroutine(c, req.URL, reqBody, respChan)

		// call upstream
		resp, err := http.DefaultClient.Do(req)
		if err != nil { http.Error(w, err.Error(), http.StatusBadGateway); return }
		defer resp.Body.Close()

		for k, vs := range resp.Header { for _, h := range vs { w.Header().Add(k, h) } }
		w.WriteHeader(resp.StatusCode)

		// stream + capture response
		respBody := streamAndCapture(w, resp.Body, c.maxCapture)
		respChan <- respBody      // hand body to coroutine
		close(respChan)           // signal done

		always(tag, req.URL.Path, "status:", resp.StatusCode, "elapsed:", time.Since(start))
	}), nil
}

/* ────────── coroutine sender ────────── */

func trackCoroutine(c *cfg, urlObj *url.URL, reqBody []byte, respCh <-chan []byte) {
	respBody := <-respCh // waits only until body copied to client

	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()

	// build payload (pooled buffer)
	buf := bufPool.Get().(*bytes.Buffer); buf.Reset()
	buf.WriteString("{$responseBody}"); buf.Write(respBody); buf.WriteString("{/responseBody},")
	buf.WriteString("{$requestBody}");  buf.Write(reqBody);  buf.WriteString("{/requestBody},")
	buf.WriteString("{$requestQuery}"); buf.WriteString(urlObj.RawQuery); buf.WriteString("{/requestQuery},")
	buf.WriteString("{$requestUrl}");   buf.WriteString(urlObj.String()); buf.WriteString("{/requestUrl}")
	payload := buf.String(); bufPool.Put(buf)

	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, c.url.String(), strings.NewReader(payload))
	req.Header.Set("Content-Type", "text/plain")

	if _, err := http.DefaultClient.Do(req); err != nil {
		vdbg(c, "POST failed:", err)
	} else {
		vdbg(c, "POST ok (", len(payload), "B )")
	}
}

/* ────────── helpers ────────── */

func captureBody(rc *io.ReadCloser, max int) []byte {
	if rc == nil || *rc == nil { return nil }
	b, _ := io.ReadAll(*rc); (*rc).Close(); *rc = io.NopCloser(bytes.NewReader(b))
	if len(b) > max { return b[:max] }
	return b
}
func streamAndCapture(dst io.Writer, src io.Reader, max int) []byte {
	if max <= 0 { io.Copy(dst, src); return nil }
	buf := slicePool.Get().([]byte)[:0]
	tee := io.TeeReader(io.LimitedReader{R: src, N: int64(max)}, &sliceWriter{&buf})
	io.Copy(dst, tee)
	defer slicePool.Put(buf[:0])
	return buf
}
type sliceWriter struct{ buf *[]byte }
func (s *sliceWriter) Write(p []byte) (int, error) { *s.buf = append(*s.buf, p...); return len(p), nil }

/* ────────── KrakenD Logger iface ────────── */
type Logger interface {
	Debug(v ...interface{})
	Info(v ...interface{})
	Warning(v ...interface{})
	Error(v ...interface{})
	Critical(v ...interface{})
	Fatal(v ...interface{})
}
