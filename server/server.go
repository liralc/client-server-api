// Command server expõe GET /cotacao na porta 8080. A cada requisição consulta
// a cotação USD-BRL na AwesomeAPI (timeout de 200ms), persiste o resultado no
// SQLite (timeout de 10ms) e devolve o valor ao cliente em JSON.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "modernc.org/sqlite" // driver SQLite em Go puro (dispensa CGO)
)

const (
	listenAddr = ":8080"
	quoteAPI   = "https://economia.awesomeapi.com.br/json/last/USD-BRL"
	dbPath     = "cotacoes.db"

	apiTimeout = 200 * time.Millisecond
	dbTimeout  = 10 * time.Millisecond
)

// Quote espelha o objeto USDBRL retornado pela AwesomeAPI.
type Quote struct {
	Code       string `json:"code"`
	Codein     string `json:"codein"`
	Name       string `json:"name"`
	High       string `json:"high"`
	Low        string `json:"low"`
	VarBid     string `json:"varBid"`
	PctChange  string `json:"pctChange"`
	Bid        string `json:"bid"`
	Ask        string `json:"ask"`
	Timestamp  string `json:"timestamp"`
	CreateDate string `json:"create_date"`
}

type apiResponse struct {
	USDBRL Quote `json:"USDBRL"`
}

// QuoteResponse é o contrato JSON exposto pelo endpoint /cotacao.
type QuoteResponse struct {
	Bid string `json:"bid"`
}

type errorResponse struct {
	Error string `json:"error"`
}

// QuoteFetcher busca a cotação atual em uma API externa.
type QuoteFetcher struct {
	client *http.Client
	url    string
}

func (f *QuoteFetcher) Fetch(ctx context.Context) (Quote, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.url, nil)
	if err != nil {
		return Quote{}, fmt.Errorf("criando requisição: %w", err)
	}

	resp, err := f.client.Do(req)
	if err != nil {
		return Quote{}, fmt.Errorf("chamando API de câmbio: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return Quote{}, fmt.Errorf("API de câmbio retornou status %d", resp.StatusCode)
	}

	var body apiResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return Quote{}, fmt.Errorf("decodificando resposta da API: %w", err)
	}
	if body.USDBRL.Bid == "" {
		return Quote{}, errors.New("resposta da API sem o campo bid")
	}
	return body.USDBRL, nil
}

// QuoteRepository persiste cotações no SQLite.
type QuoteRepository struct {
	db     *sql.DB
	insert *sql.Stmt
}

const schema = `
CREATE TABLE IF NOT EXISTS quotes (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	code        TEXT NOT NULL,
	codein      TEXT NOT NULL,
	name        TEXT NOT NULL,
	high        TEXT NOT NULL,
	low         TEXT NOT NULL,
	var_bid     TEXT NOT NULL,
	pct_change  TEXT NOT NULL,
	bid         TEXT NOT NULL,
	ask         TEXT NOT NULL,
	timestamp   TEXT NOT NULL,
	create_date TEXT NOT NULL,
	created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);`

const insertQuote = `
INSERT INTO quotes (code, codein, name, high, low, var_bid, pct_change, bid, ask, timestamp, create_date)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

// NewQuoteRepository abre o banco, cria o schema e prepara o INSERT antecipadamente,
// para que o caminho quente caiba no orçamento de 10ms.
func NewQuoteRepository(ctx context.Context, dsn string) (*QuoteRepository, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("abrindo banco: %w", err)
	}
	// SQLite admite um único escritor; serializar evita SQLITE_BUSY.
	db.SetMaxOpenConns(1)

	for _, stmt := range []string{
		"PRAGMA journal_mode = WAL;",
		"PRAGMA synchronous = NORMAL;",
		schema,
	} {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			db.Close()
			return nil, fmt.Errorf("inicializando banco: %w", err)
		}
	}

	insert, err := db.PrepareContext(ctx, insertQuote)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("preparando insert: %w", err)
	}
	return &QuoteRepository{db: db, insert: insert}, nil
}

func (r *QuoteRepository) Save(ctx context.Context, q Quote) error {
	_, err := r.insert.ExecContext(ctx,
		q.Code, q.Codein, q.Name, q.High, q.Low, q.VarBid,
		q.PctChange, q.Bid, q.Ask, q.Timestamp, q.CreateDate,
	)
	if err != nil {
		return fmt.Errorf("inserindo cotação: %w", err)
	}
	return nil
}

func (r *QuoteRepository) Close() error {
	r.insert.Close()
	return r.db.Close()
}

// QuoteHandler atende GET /cotacao.
type QuoteHandler struct {
	fetcher    *QuoteFetcher
	repo       *QuoteRepository
	logger     *slog.Logger
	apiTimeout time.Duration
	dbTimeout  time.Duration
}

func (h *QuoteHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	apiCtx, cancel := context.WithTimeout(r.Context(), h.apiTimeout)
	defer cancel()

	quote, err := h.fetcher.Fetch(apiCtx)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			h.logger.Error("timeout ao consultar API de câmbio", "timeout", h.apiTimeout, "err", err)
			writeJSON(w, http.StatusGatewayTimeout, errorResponse{"timeout ao consultar API de câmbio"})
			return
		}
		h.logger.Error("falha ao consultar API de câmbio", "err", err)
		writeJSON(w, http.StatusBadGateway, errorResponse{"falha ao consultar API de câmbio"})
		return
	}

	// WithoutCancel: a persistência obedece apenas ao próprio timeout de 10ms,
	// não sendo abortada se o cliente desconectar.
	dbCtx, cancelDB := context.WithTimeout(context.WithoutCancel(r.Context()), h.dbTimeout)
	defer cancelDB()

	if err := h.repo.Save(dbCtx, quote); err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(dbCtx.Err(), context.DeadlineExceeded) {
			h.logger.Error("timeout ao persistir cotação", "timeout", h.dbTimeout, "err", err)
		} else {
			h.logger.Error("falha ao persistir cotação", "err", err)
		}
		writeJSON(w, http.StatusInternalServerError, errorResponse{"falha ao persistir cotação"})
		return
	}

	h.logger.Info("cotação registrada", "bid", quote.Bid)
	writeJSON(w, http.StatusOK, QuoteResponse{Bid: quote.Bid})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("escrevendo resposta", "err", err)
	}
}

func newMux(h http.Handler) *http.ServeMux {
	mux := http.NewServeMux()
	mux.Handle("GET /cotacao", h)
	return mux
}

func run(ctx context.Context, logger *slog.Logger) error {
	repo, err := NewQuoteRepository(ctx, dbPath)
	if err != nil {
		return err
	}
	defer repo.Close()

	handler := &QuoteHandler{
		fetcher:    &QuoteFetcher{client: &http.Client{}, url: quoteAPI},
		repo:       repo,
		logger:     logger,
		apiTimeout: apiTimeout,
		dbTimeout:  dbTimeout,
	}

	srv := &http.Server{
		Addr:              listenAddr,
		Handler:           newMux(handler),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("servidor ouvindo", "addr", listenAddr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}

	logger.Info("encerrando servidor")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, logger); err != nil {
		logger.Error("servidor finalizado com erro", "err", err)
		os.Exit(1)
	}
}
