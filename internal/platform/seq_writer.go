package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"
)

// seqWriter batches zerolog JSON lines, remaps them to CLEF (Compact Log
// Event Format), and flushes them asynchronously.
type seqWriter struct {
	url    string
	client *http.Client

	mu    sync.Mutex
	batch bytes.Buffer
}

const (
	seqFlushInterval = time.Second
	seqMaxBatchLines = 200
)

// newSeqWriter creates a seqWriter that posts log events to seqURL.
func newSeqWriter(seqURL string) *seqWriter {
	w := &seqWriter{
		url:    seqURL + "/api/events/raw?clef",
		client: &http.Client{Timeout: 5 * time.Second},
	}
	go w.flushLoop()
	return w
}

// Write converts a zerolog JSON line to CLEF and adds it to the batch.
func (w *seqWriter) Write(p []byte) (int, error) {
	clef, err := toCLEF(p)
	if err != nil {
		return len(p), nil //nolint:nilerr // drop malformed lines, never propagate
	}

	w.mu.Lock()
	w.batch.Write(clef)
	w.batch.WriteByte('\n')
	lines := w.batch.Len()
	w.mu.Unlock()

	if lines >= seqMaxBatchLines {
		w.flush()
	}
	return len(p), nil
}

// flushLoop periodically flushes the batch to Seq.
func (w *seqWriter) flushLoop() {
	ticker := time.NewTicker(seqFlushInterval)
	defer ticker.Stop()
	for range ticker.C {
		w.flush()
	}
}

// flush sends the current batch to Seq and resets it.
func (w *seqWriter) flush() {
	w.mu.Lock()
	if w.batch.Len() == 0 {
		w.mu.Unlock()
		return
	}
	payload := make([]byte, w.batch.Len())
	copy(payload, w.batch.Bytes())
	w.batch.Reset()
	w.mu.Unlock()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, w.url, bytes.NewReader(payload))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/vnd.serilog.clef")

	resp, err := w.client.Do(req)
	if err != nil {
		return
	}
	_ = resp.Body.Close()
}

// toCLEF renames zerolog's default field names to their CLEF equivalents.
func toCLEF(line []byte) ([]byte, error) {
	var fields map[string]any
	if err := json.Unmarshal(line, &fields); err != nil {
		return nil, err
	}

	clef := make(map[string]any, len(fields))
	for k, v := range fields {
		switch k {
		case "time":
			clef["@t"] = v
		case "level":
			clef["@l"] = v
		case "message":
			clef["@m"] = v
		default:
			clef[k] = v
		}
	}
	return json.Marshal(clef)
}
