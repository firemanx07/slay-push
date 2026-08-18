package platform

import (
	"bytes"
	"encoding/json"
	"net/http"
	"sync"
	"time"
)

// seqWriter batches zerolog JSON lines, remaps them to the minimal CLEF
// (Compact Log Event Format) shape Seq's raw ingestion endpoint expects,
// and flushes them asynchronously so logging never blocks on an HTTP
// round-trip per line.
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

func newSeqWriter(seqURL string) *seqWriter {
	w := &seqWriter{
		url:    seqURL + "/api/events/raw?clef",
		client: &http.Client{Timeout: 5 * time.Second},
	}
	go w.flushLoop()
	return w
}

func (w *seqWriter) Write(p []byte) (int, error) {
	clef, err := toCLEF(p)
	if err != nil {
		// Never let a malformed line take down logging; drop it.
		return len(p), nil
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

func (w *seqWriter) flushLoop() {
	ticker := time.NewTicker(seqFlushInterval)
	defer ticker.Stop()
	for range ticker.C {
		w.flush()
	}
}

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

	// Best-effort: a Seq hiccup must never affect application behavior.
	resp, err := w.client.Post(w.url, "application/vnd.serilog.clef", bytes.NewReader(payload))
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
