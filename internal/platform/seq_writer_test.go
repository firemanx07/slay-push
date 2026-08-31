package platform

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

func TestToCLEF(t *testing.T) {
	t.Run("renames known fields, passes through others", func(t *testing.T) {
		in := []byte(`{"time":"2024-01-01T00:00:00Z","level":"info","message":"hi","other":"x"}`)
		out, err := toCLEF(in)
		if err != nil {
			t.Fatalf("toCLEF: %v", err)
		}
		var fields map[string]any
		if err := json.Unmarshal(out, &fields); err != nil {
			t.Fatalf("unmarshal output: %v", err)
		}
		if fields["@t"] != "2024-01-01T00:00:00Z" || fields["@l"] != "info" || fields["@m"] != "hi" || fields["other"] != "x" {
			t.Errorf("got %+v, want renamed @t/@l/@m plus passthrough other", fields)
		}
	})

	t.Run("malformed JSON returns an error", func(t *testing.T) {
		if _, err := toCLEF([]byte(`{not json`)); err == nil {
			t.Fatal("expected an error for malformed JSON")
		}
	})
}

func TestSeqWriter_Write_MalformedLineDropped(t *testing.T) {
	w := &seqWriter{url: "http://unused", client: &http.Client{}}
	n, err := w.Write([]byte(`{not json`))
	if err != nil {
		t.Fatalf("Write returned an error, want nil (malformed lines are dropped silently): %v", err)
	}
	if n != len(`{not json`) {
		t.Errorf("n = %d, want %d (io.Writer contract: report all bytes consumed)", n, len(`{not json`))
	}
	w.mu.Lock()
	batchLen := w.batch.Len()
	w.mu.Unlock()
	if batchLen != 0 {
		t.Errorf("batch length = %d, want 0 (malformed line should not be added)", batchLen)
	}
}

func TestSeqWriter_Flush_EmptyBatchIsNoop(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
	}))
	defer server.Close()

	w := &seqWriter{url: server.URL, client: server.Client()}
	w.flush()
	if requests != 0 {
		t.Errorf("flush() on an empty batch made %d request(s), want 0", requests)
	}
}

func TestSeqWriter_WriteAndFlush(t *testing.T) {
	var mu sync.Mutex
	var gotBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
			return
		}
		mu.Lock()
		gotBody = body
		mu.Unlock()
	}))
	defer server.Close()

	sw := &seqWriter{url: server.URL, client: server.Client()}
	line := []byte(`{"time":"2024-01-01T00:00:00Z","level":"info","message":"hi"}`)
	if _, err := sw.Write(line); err != nil {
		t.Fatalf("Write: %v", err)
	}
	sw.flush()

	mu.Lock()
	defer mu.Unlock()
	if len(gotBody) == 0 {
		t.Fatal("expected flush to POST a non-empty CLEF batch")
	}
	var fields map[string]any
	if err := json.Unmarshal(gotBody, &fields); err != nil {
		t.Fatalf("posted body is not valid JSON: %v (%s)", err, gotBody)
	}
	if fields["@m"] != "hi" {
		t.Errorf("posted body = %+v, want @m=%q", fields, "hi")
	}
}

func TestSeqWriter_AutoFlushOnBatchThreshold(t *testing.T) {
	var flushes int
	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		flushes++
		mu.Unlock()
	}))
	defer server.Close()

	sw := &seqWriter{url: server.URL, client: server.Client()}
	line := []byte(`{"time":"t","level":"info","message":"m"}`)
	for i := 0; i < seqMaxBatchLines; i++ {
		if _, err := sw.Write(line); err != nil {
			t.Fatalf("Write %d: %v", i, err)
		}
	}

	mu.Lock()
	defer mu.Unlock()
	if flushes == 0 {
		t.Error("expected Write to trigger an automatic flush once the batch threshold was crossed")
	}
}
