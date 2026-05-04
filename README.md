# OMP Session Backend

Go API for a local read-only OMP session viewer.

The server reads OMP session data from `~/.omp/agent` and exposes a localhost HTTP API for the frontend in `OMP-session-frontend`.

## Safety model

- Opens OMP SQLite databases in read-only mode
- Reads JSONL rollout files without mutating them
- Binds to `127.0.0.1:8080` by default
- Does not execute `omp`; it only returns copyable resume commands

## Requirements

- Go 1.26+
- Local OMP data under `~/.omp/agent`

## Development

```bash
./scripts/dev.sh
```

The script builds `bin/omp-session-viewer-server`, runs that binary, and exits cleanly on `Ctrl+C`. You can still use `make dev`, but some terminals/runners report `make` itself as interrupted; use the script directly if you want a clean zero-exit stop.

Equivalent explicit command:

```bash
go build -o bin/omp-session-viewer-server ./cmd/server
./bin/omp-session-viewer-server -addr 127.0.0.1:8080 -omp-root ~/.omp/agent -frontend-origin http://localhost:5173
```

## Make targets

```bash
make help
make dev
make build
make test
make fmt
make check
make clean
```

Configurable variables:

```bash
make dev ADDR=127.0.0.1:18084 OMP_ROOT=~/.omp/agent FRONTEND_ORIGIN=http://localhost:5173
make build BINARY=bin/omp-session-viewer-server
```

## API

- `GET /api/health`
- `GET /api/sessions`
- `GET /api/sessions/{id}`
- `GET /api/sessions/{id}/messages`
- `GET /api/cwds`

Useful session query parameters:

- `q`: token search
- `cwd`: working directory filter
- `sourceKind`: source filter, for example `cli`
- `includeEmptyMessages=true`: include sessions with zero readable messages
- `messageCountBucket`: `0-25`, `25-75`, `75-150`, or `150+`
- `limit` and `offset`: pagination

## Checks

```bash
make check
```

## Related repository

Frontend UI: https://github.com/ValTM/OMP-session-frontend
