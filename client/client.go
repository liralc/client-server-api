// Command client solicita a cotação ao servidor local (timeout de 300ms)
// e grava o valor do bid em cotacao.txt no formato "Dólar: {valor}".
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"
)

const (
	serverURL      = "http://localhost:8080/cotacao"
	outputFile     = "cotacao.txt"
	requestTimeout = 300 * time.Millisecond
)

type quoteResponse struct {
	Bid string `json:"bid"`
}

// fetchBid obtém do servidor apenas o valor atual do câmbio (bid).
func fetchBid(ctx context.Context, client *http.Client, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("criando requisição: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("chamando servidor: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("servidor retornou status %d", resp.StatusCode)
	}

	var body quoteResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", fmt.Errorf("decodificando resposta: %w", err)
	}
	if body.Bid == "" {
		return "", errors.New("resposta sem o campo bid")
	}
	return body.Bid, nil
}

func saveQuote(path, bid string) error {
	content := fmt.Sprintf("Dólar: %s\n", bid)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("gravando %s: %w", path, err)
	}
	return nil
}

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()

	bid, err := fetchBid(ctx, http.DefaultClient, serverURL)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			logger.Error("timeout ao obter cotação do servidor", "timeout", requestTimeout, "err", err)
		} else {
			logger.Error("falha ao obter cotação do servidor", "err", err)
		}
		os.Exit(1)
	}

	if err := saveQuote(outputFile, bid); err != nil {
		logger.Error("falha ao salvar cotação", "err", err)
		os.Exit(1)
	}
	logger.Info("cotação salva", "arquivo", outputFile, "bid", bid)
}
