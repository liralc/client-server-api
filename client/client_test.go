package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFetchBid_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `{"bid":"5.2172"}`)
	}))
	defer srv.Close()

	bid, err := fetchBid(context.Background(), srv.Client(), srv.URL)
	if err != nil {
		t.Fatalf("fetchBid: %v", err)
	}
	if bid != "5.2172" {
		t.Errorf("bid = %q, want %q", bid, "5.2172")
	}
}

func TestFetchBid_Timeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(200 * time.Millisecond):
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	_, err := fetchBid(ctx, srv.Client(), srv.URL)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context.DeadlineExceeded", err)
	}
}

func TestFetchBid_NonOKStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusGatewayTimeout)
	}))
	defer srv.Close()

	if _, err := fetchBid(context.Background(), srv.Client(), srv.URL); err == nil {
		t.Fatal("esperava erro para status != 200")
	}
}

func TestSaveQuote(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cotacao.txt")
	if err := saveQuote(path, "5.2172"); err != nil {
		t.Fatalf("saveQuote: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if want := "Dólar: 5.2172\n"; string(got) != want {
		t.Errorf("conteúdo = %q, want %q", got, want)
	}
}
