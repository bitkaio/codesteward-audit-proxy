package proxy

import (
	"io"
	"net/http"
	"strings"
)

// tapReadCloser wraps an io.Reader and calls closeFunc when Close is called.
type tapReadCloser struct {
	io.Reader
	closeFunc func() error
}

func (t *tapReadCloser) Close() error {
	return t.closeFunc()
}

// TapBody wraps resp.Body so that:
//  1. All bytes read from resp.Body are forwarded to the caller immediately
//     (critical path — no added latency).
//  2. A copy of every byte is written to a pipe that an audit goroutine reads
//     asynchronously.
//
// onComplete is called in a goroutine once the full body has been consumed.
// It receives the raw body bytes and a flag indicating whether the response
// was a streaming (SSE) response.
//
// Ownership of resp.Body is transferred to the new tapReadCloser; the original
// body is closed inside the replacement's Close method.
func TapBody(resp *http.Response, onComplete func(body []byte, isStream bool)) {
	isStream := strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream")

	pr, pw := io.Pipe()
	tee := io.TeeReader(resp.Body, pw)
	origBody := resp.Body

	resp.Body = &tapReadCloser{
		Reader: tee,
		closeFunc: func() error {
			// Closing pw signals the goroutine's io.ReadAll to return.
			pw.Close()
			return origBody.Close()
		},
	}

	go func() {
		defer pr.Close()
		body, _ := io.ReadAll(pr)
		onComplete(body, isStream)
	}()
}
