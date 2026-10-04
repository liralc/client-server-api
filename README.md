# Client-Server-API

Desafio Go: dois programas (`server` e `client`) que trocam a cotação do dólar
respeitando timeouts estritos com o pacote `context`.

| Etapa                                   | Timeout |
|-----------------------------------------|---------|
| Server → API de câmbio (AwesomeAPI)     | 200ms   |
| Server → persistência no SQLite         | 10ms    |
| Client → Server (`/cotacao`)            | 300ms   |

## Estrutura

```
.
├── server/
│   ├── server.go        # HTTP :8080, GET /cotacao, persistência em SQLite
│   └── server_test.go
├── client/
│   ├── client.go        # consome /cotacao e grava cotacao.txt
│   └── client_test.go
├── go.mod
└── go.sum
```

## Requisitos

- Go 1.27+ (versão definida no `go.mod`)
- Não é preciso CGO nem gcc: o driver SQLite é o [`modernc.org/sqlite`](https://pkg.go.dev/modernc.org/sqlite), escrito em Go puro.

## Como executar

Na raiz do projeto, baixe as dependências:

```bash
go mod download
```

**Terminal 1: servidor**

```bash
go run ./server
```

O servidor sobe em `http://localhost:8080` e cria o banco `cotacoes.db` (tabela `quotes`)
no diretório atual. Para encerrar, use `Ctrl+C` (o servidor faz graceful shutdown).

**Terminal 2: cliente**

```bash
go run ./client
cat cotacao.txt
# Dólar: 5.2172
```

Também é possível consultar o endpoint diretamente:

```bash
curl http://localhost:8080/cotacao
# {"bid":"5.2172"}
```

## Comportamento

### Server

1. Recebe `GET /cotacao` (outros métodos recebem `405`).
2. Consulta `https://economia.awesomeapi.com.br/json/last/USD-BRL` com timeout de **200ms**.
3. Persiste a cotação completa no SQLite com timeout de **10ms**.
4. Responde `200` com `{"bid": "<valor>"}`.

Erros e respostas:

| Situação                         | Status HTTP | Log no console                          |
|----------------------------------|-------------|-----------------------------------------|
| Timeout da API externa (200ms)   | `504`       | `timeout ao consultar API de câmbio`    |
| Outra falha da API externa       | `502`       | `falha ao consultar API de câmbio`      |
| Timeout do banco (10ms)          | `500`       | `timeout ao persistir cotação`          |
| Outra falha do banco             | `500`       | `falha ao persistir cotação`            |

> A API pública pode demorar mais de 200ms dependendo da rede. Nesse caso o
> servidor registra o timeout no log e responde `504`, como pede o desafio.

### Client

1. Faz `GET http://localhost:8080/cotacao` com timeout de **300ms**.
2. Lê apenas o campo `bid` da resposta.
3. Grava `cotacao.txt` no formato `Dólar: {valor}`.
4. Em caso de timeout, registra `timeout ao obter cotação do servidor` no console e sai com código `1`.

## Testes

```bash
go test -race ./...
```

Os testes usam `httptest` para simular a API externa e o servidor, cobrindo sucesso,
timeouts (API, banco e cliente), status de erro e o formato do arquivo gerado.
