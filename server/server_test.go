package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

const fakeAPIBody = `{"USDBRL":{"code":"USD","codein":"BRL","name":"Dólar Americano/Real Brasileiro","high":"5.2376","low":"5.1987","varBid":"-0.0099","pctChange":"-0.189396","bid":"5.2172","ask":"5.2183","timestamp":"1790975912","create_date":"2026-10-02 18:18:32"}}`

func newTestHandler(t *testing.T, upstream http.HandlerFunc, apiTimeout time.Duration) (*QuoteHandler, *QuoteRepository) {
	t.Helper()

	api := httptest.NewServer(upstream)
	t.Cleanup(api.Close)

	repo, err := NewQuoteRepository(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewQuoteRepository: %v", err)
	}
	t.Cleanup(func() { repo.Close() })

	return &QuoteHandler{
		fetcher:    &QuoteFetcher{client: api.Client(), url: api.URL},
		repo:       repo,
		logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		apiTimeout: apiTimeout,
		dbTimeout:  time.Second, // folga para ambientes de CI lentos
	}, repo
}

func countQuotes(t *testing.T, repo *QuoteRepository) int {
	t.Helper()
	var n int
	if err := repo.db.QueryRow("SELECT COUNT(*) FROM quotes").Scan(&n); err != nil {
		t.Fatalf("contando cotações: %v", err)
	}
	return n
}

func TestQuoteHandler_Success(t *testing.T) {
	h, repo := newTestHandler(t, func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, fakeAPIBody)
	}, time.Second)

	rec := httptest.NewRecorder()
	newMux(h).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/cotacao", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var got QuoteResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Bid != "5.2172" {
		t.Errorf("bid = %q, want %q", got.Bid, "5.2172")
	}
	if n := countQuotes(t, repo); n != 1 {
		t.Errorf("cotações persistidas = %d, want 1", n)
	}
}

func TestQuoteHandler_APITimeout(t *testing.T) {
	h, repo := newTestHandler(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(200 * time.Millisecond):
		case <-r.Context().Done():
		}
		io.WriteString(w, fakeAPIBody)
	}, 20*time.Millisecond)

	rec := httptest.NewRecorder()
	newMux(h).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/cotacao", nil))

	if rec.Code != http.StatusGatewayTimeout {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusGatewayTimeout)
	}
	if n := countQuotes(t, repo); n != 0 {
		t.Errorf("cotações persistidas = %d, want 0", n)
	}
}

func TestQuoteHandler_DBTimeout(t *testing.T) {
	h, repo := newTestHandler(t, func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, fakeAPIBody)
	}, time.Second)
	h.dbTimeout = time.Nanosecond

	rec := httptest.NewRecorder()
	newMux(h).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/cotacao", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
	if n := countQuotes(t, repo); n != 0 {
		t.Errorf("cotações persistidas = %d, want 0", n)
	}
}

func TestQuoteHandler_UpstreamError(t *testing.T) {
	h, _ := newTestHandler(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}, time.Second)

	rec := httptest.NewRecorder()
	newMux(h).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/cotacao", nil))

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadGateway)
	}
}

func TestMux_MethodNotAllowed(t *testing.T) {
	h, _ := newTestHandler(t, func(http.ResponseWriter, *http.Request) {}, time.Second)

	rec := httptest.NewRecorder()
	newMux(h).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/cotacao", nil))

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
}
