package http

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type decodeTarget struct {
	Foo string `json:"foo"`
}

func TestDecodeJSON(t *testing.T) {
	t.Run("valid body decodes and returns true", func(t *testing.T) {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/", strings.NewReader(`{"foo":"bar"}`))
		w := httptest.NewRecorder()
		var dst decodeTarget
		if ok := decodeJSON(w, req, &dst); !ok {
			t.Fatalf("decodeJSON returned false for a valid body (status %d)", w.Code)
		}
		if dst.Foo != "bar" {
			t.Errorf("Foo = %q, want %q", dst.Foo, "bar")
		}
	})

	t.Run("malformed JSON returns false with 400", func(t *testing.T) {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/", strings.NewReader(`{not json`))
		w := httptest.NewRecorder()
		var dst decodeTarget
		if ok := decodeJSON(w, req, &dst); ok {
			t.Fatal("decodeJSON returned true for malformed JSON")
		}
		if w.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
		}
	})

	t.Run("unknown field returns false with 400", func(t *testing.T) {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/", strings.NewReader(`{"foo":"bar","unexpected":true}`))
		w := httptest.NewRecorder()
		var dst decodeTarget
		if ok := decodeJSON(w, req, &dst); ok {
			t.Fatal("decodeJSON returned true for a body with an unknown field")
		}
		if w.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
		}
	})

	t.Run("oversized body returns false with 413", func(t *testing.T) {
		big := bytes.Repeat([]byte("a"), maxRequestBodyBytes+1)
		body := `{"foo":"` + string(big) + `"}`
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/", strings.NewReader(body))
		w := httptest.NewRecorder()
		var dst decodeTarget
		if ok := decodeJSON(w, req, &dst); ok {
			t.Fatal("decodeJSON returned true for an oversized body")
		}
		if w.Code != http.StatusRequestEntityTooLarge {
			t.Errorf("status = %d, want %d", w.Code, http.StatusRequestEntityTooLarge)
		}
	})
}
