// Coroutine-based KrakenD client plugin that mirrors request / response data to
// an external tracking endpoint without blocking user traffic.
//
// • Symbol / name  : krakend-trace-plugin
// • Params (same keys, defaults preserved)
//     - tracking_url   (mandatory)
//     - timeout_ms     (default 2000 ms)
//     - max_capture_kb (default 256 KB)
//     - verbose        (default false)
// • Behaviour
//     1. Captures request body (clipped to max_capture_kb).
//     2. Streams response to caller while capturing up to max_capture_kb.
//     3. Spawns ONE goroutine that builds the payload and posts it under its
//        own deadline (never blocks the handler).
// • Payload format sent to tracking_url (Content-Type text/plain):
//     {$responseBody}<body>{/responseBody},
//     {$requestBody}<body>{/requestBody},
//     {$requestQuery}<raw query>{/requestQuery},
//     {$requestUrl}<full url>{/requestUrl}
//
// Build:
//   CGO_ENABLED=0 go build -trimpath -buildmode=plugin -o krakend-trace-plugin.so .
//
// SPDX-License-Identifier: Apache-2.0
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

/* ─────────────────── defaults ─────────────────── */

const (
	defTimeoutMS    = 2_000           // per-event deadline (ms)
	defMaxCaptureKB = 256             // body capture limit
	headerReqID     = "X-Request-Id"  // correlation header
	tag             = "[krakend-trace-plugin]"
)

/* ─────────────────── configuration ─────────────────── */

type cfg struct {
	url        *url.URL
	timeout    time.Duration
	maxCapture int
	verbose    bool
}

/* ─────────────────── globals ─────────────────── */

var (
	ClientRegisterer = registerer("krakend-trace-plugin")
	logger           Logger
)

type registerer string

/* ───────── helpers for optional DEBUG ───────── */

func vdbg(c *cfg, v ...interface{}) {
	if c.verbose && logger != nil {
		logger.Debug(append([]interface{}{tag}, v...)...)
	}
}
func always(v ...interface{}) {
	if logger != nil {
		logger.Debug(append([]interface{}{tag}, v...)...)
	}
}

/* ───────── tiny object pools ───────── */

var bufPool = sync.Pool{New: func() any { return &bytes.Buffer{} }}
var slicePool = sync.Pool{New: func() any { return make([]byte, 0, defMaxCaptureKB*1024) }}

/* ───────── KrakenD hooks ───────── */

func (registerer) RegisterLogger(v interface{}) {
	if l, ok := v.(Logger); ok {
		logger = l
		always("logger injected")
	}
}

func (r registerer) RegisterClients(register func(
	name string,
	handler func(context.Context, map[string]interface{}) (http.Handler, error),
)) {
	register(string(r), r.registerClients)
}

/* ───────── registerClients ───────── */

func (r registerer) registerClients(_ context.Context, extra map[string]interface{}) (http.Handler, error) {
	block := extra[string(r)].(map[string]interface{})

	// mandatory tracking_url
	target, err := url.ParseRequestURI(block["tracking_url"].(string))
	if err != nil {
		return nil, fmt.Errorf("%s invalid tracking_url: %w", tag, err)
	}

	c := &cfg{
		url:        target,
		timeout:    defTimeoutMS * time.Millisecond,
		maxCapture: defMaxCaptureKB * 1024,
		verbose:    false,
	}
	if v, ok := block["timeout_ms"].(float64); ok && v > 0 {
		c.timeout = time.Duration(v) * time.Millisecond
	}
	if v, ok := block["max_capture_kb"].(float64); ok && v > 0 {
		c.maxCapture = int(v * 1024)
	}
	if v, ok := block["verbose"].(bool); ok {
		c.verbose = v
	}

	logger.Info(tag, "config →", c.url, "timeout:", c.timeout,
		"max_cap:", c.maxCapture, "verbose:", c.verbose)

	/* ───────── proxy handler ───────── */
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		start := time.Now()

		// capture request body (clipped)
		reqBody := captureBody(&req.Body, c.maxCapture)
		vdbg(c, "reqB:", len(reqBody))

		// channel passes captured resp body to coroutine
		respCh := make(chan []byte, 1)

		// coroutine: build payload & POST (non-blocking)
		go trackingCoroutine(c, req.URL, reqBody, respCh)

		// call upstream
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()

		// propagate headers & status
		for k, vs := range resp.Header {
			for _, h := range vs {
				w.Header().Add(k, h)
			}
		}
		w.WriteHeader(resp.StatusCode)

		// stream response to client & capture slice
		respBody := streamAndCapture(w, resp.Body, c.maxCapture)
		respCh <- respBody
		close(respCh)

		always(tag, req.URL.Path, "status:", resp.StatusCode, "elapsed:", time.Since(start))
	}), nil
}

/* ───────── coroutine sender ───────── */

func trackingCoroutine(c *cfg, urlObj *url.URL, reqBody []byte, respCh <-chan []byte) {
	respBody := <-respCh // waits only for capture to finish

	// build payload with pooled buffer
	buf := bufPool.Get().(*bytes.Buffer)
	buf.Reset()
	buf.WriteString("{$responseBody}")
	buf.Write(respBody)
	buf.WriteString("{/responseBody},{$requestBody}")
	buf.Write(reqBody)
	buf.WriteString("{/requestBody},{$requestQuery}")
	buf.WriteString(urlObj.RawQuery)
	buf.WriteString("{/requestQuery},{$requestUrl}")
	buf.WriteString(urlObj.String())
	buf.WriteString("{/requestUrl}")
	payload := buf.String()
	bufPool.Put(buf)

	// detached POST with per-event timeout
	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()

	r, _ := http.NewRequestWithContext(ctx, http.MethodPost, c.url.String(), strings.NewReader(payload))
	r.Header.Set("Content-Type", "text/plain")

	if _, err := http.DefaultClient.Do(r); err != nil {
		vdbg(c, "POST failed:", err)
	} else {
		vdbg(c, "POST ok (", len(payload), "B)")
	}
}

/* ───────── helpers ───────── */

func captureBody(rc *io.ReadCloser, max int) []byte {
	if rc == nil || *rc == nil {
		return nil
	}
	all, _ := io.ReadAll(*rc)
	(*rc).Close()
	*rc = io.NopCloser(bytes.NewReader(all))
	if len(all) > max {
		return all[:max]
	}
	return all
}

func streamAndCapture(dst io.Writer, src io.Reader, max int) []byte {
	if max <= 0 {
		io.Copy(dst, src)
		return nil
	}

	buf := slicePool.Get().([]byte)[:0]
	lr := &io.LimitedReader{R: src, N: int64(max)} // pointer implements Reader
	tee := io.TeeReader(lr, &sliceWriter{&buf})
	io.Copy(dst, tee)
	defer slicePool.Put(buf[:0])
	return buf
}

type sliceWriter struct{ buf *[]byte }

func (s *sliceWriter) Write(p []byte) (int, error) {
	*s.buf = append(*s.buf, p...)
	return len(p), nil
}

/* ───────── KrakenD logger interface ───────── */

type Logger interface {
	Debug(v ...interface{})
	Info(v ...interface{})
	Warning(v ...interface{})
	Error(v ...interface{})
	Critical(v ...interface{})
	Fatal(v ...interface{})
}
