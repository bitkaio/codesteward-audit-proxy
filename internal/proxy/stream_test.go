package proxy

import (
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

func makeResp(body string, contentType string) *http.Response {
	return &http.Response{
		StatusCode: 200,
		Header:     http.Header{"Content-Type": []string{contentType}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

// TestTapBody_ForwardsBodyUnchanged verifies that the wrapped body is fully
// readable and identical to the original.
func TestTapBody_ForwardsBodyUnchanged(t *testing.T) {
	const payload = "hello, streaming world"
	resp := makeResp(payload, "application/json")

	var wg sync.WaitGroup
	wg.Add(1)
	TapBody(resp, func(body []byte, isStream bool) {
		defer wg.Done()
		if string(body) != payload {
			t.Errorf("audit body: got %q, want %q", body, payload)
		}
		if isStream {
			t.Error("isStream should be false for application/json")
		}
	})

	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading wrapped body: %v", err)
	}
	if err := resp.Body.Close(); err != nil {
		t.Fatalf("closing wrapped body: %v", err)
	}

	if string(got) != payload {
		t.Errorf("forwarded body: got %q, want %q", got, payload)
	}

	// Wait for the audit goroutine to complete.
	wg.Wait()
}

// TestTapBody_DetectsStream verifies the isStream flag for SSE responses.
func TestTapBody_DetectsStream(t *testing.T) {
	resp := makeResp("data: hello\n\n", "text/event-stream")

	var gotIsStream bool
	var wg sync.WaitGroup
	wg.Add(1)
	TapBody(resp, func(_ []byte, isStream bool) {
		defer wg.Done()
		gotIsStream = isStream
	})

	io.ReadAll(resp.Body)
	resp.Body.Close()
	wg.Wait()

	if !gotIsStream {
		t.Error("isStream should be true for text/event-stream")
	}
}

// TestTapBody_LargeBody verifies that the tap works correctly for a body that
// spans many reads (simulating a chunked streaming response).
func TestTapBody_LargeBody(t *testing.T) {
	// Build a body larger than typical read buffers.
	chunk := strings.Repeat("x", 4096)
	payload := strings.Repeat(chunk, 10) // 40 KB

	resp := makeResp(payload, "text/event-stream")

	var auditBody []byte
	var wg sync.WaitGroup
	wg.Add(1)
	TapBody(resp, func(body []byte, _ bool) {
		defer wg.Done()
		auditBody = body
	})

	forwarded, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	resp.Body.Close()
	wg.Wait()

	if len(forwarded) != len(payload) {
		t.Errorf("forwarded len: got %d, want %d", len(forwarded), len(payload))
	}
	if len(auditBody) != len(payload) {
		t.Errorf("audit body len: got %d, want %d", len(auditBody), len(payload))
	}
}

// TestTapBody_CallbackCalledAfterClose verifies that the onComplete callback
// is not called until the body is fully consumed and closed.
func TestTapBody_CallbackCalledAfterClose(t *testing.T) {
	resp := makeResp("some data", "application/json")

	called := make(chan struct{})
	TapBody(resp, func(_ []byte, _ bool) {
		close(called)
	})

	// Callback must not fire before we read the body.
	select {
	case <-called:
		t.Fatal("callback fired before body was read")
	case <-time.After(20 * time.Millisecond):
	}

	io.ReadAll(resp.Body)
	resp.Body.Close()

	select {
	case <-called:
		// Good.
	case <-time.After(time.Second):
		t.Fatal("callback was not called after body close")
	}
}
