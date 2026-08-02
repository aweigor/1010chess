# Chess 10×10

A browser-based 10×10 chess variant for two players, with optional time controls and real-time board synchronization via Server-Sent Events.

## Features

- 10×10 board with full piece movement (pawns, knights, bishops, rooks, queens, kings)
- Special moves: castling (kingside & queenside), en-passant-style pawn double-push, pawn promotion
- Check, checkmate, and stalemate detection
- Optional per-player time controls (3 / 5 / 10 / 15 min or unlimited)
- Live updates — both players see moves instantly over SSE without polling
- Token-based authentication — each player gets a private token at game creation/join
- Persistent games stored in SQLite — server restarts are safe

## Tech Stack

| Layer     | Technology                          |
|-----------|-------------------------------------|
| Backend   | Go 1.25, `net/http`, `modernc.org/sqlite` |
| Real-time | Server-Sent Events (SSE)            |
| Frontend  | Vanilla HTML / CSS / JavaScript (single file SPA) |
| Container | Docker (multi-stage, distroless), Docker Compose, Nginx |

## Project Structure

```
.
├── main.go              # Server entry point; CLI flags and HTTP routing
├── engine.go            # Chess engine: board state, move generation, check/mate detection
├── handlers.go          # HTTP handlers and SSE; game lifecycle logic
├── hub.go               # SSE hub — fan-out broadcasts to connected players
├── store.go             # SQLite persistence: games and players tables
├── go.mod / go.sum      # Go module manifest
│
├── static/
│   ├── index.html       # Full frontend SPA (embedded CSS + JS)
│   └── assets/pieces/   # PNG piece images (bB, bK, bN, bP, bQ, bR, wB…)
│
├── Dockerfile           # Backend: multi-stage build → distroless runtime image
├── Dockerfile.frontend  # Frontend: Nginx serving index.html with env-var injection
├── docker-compose.yml   # Full-stack compose (backend :3002, frontend :3001)
├── nginx.conf           # Nginx config: gzip, security headers, /api/* proxy
├── docker-entrypoint.sh # Substitutes BASE_URL / API_PATH into index.html at startup
└── .env.example         # Environment variable template
```

## Getting Started (Development)

### Prerequisites

- Go 1.21+ (module-aware)
- Any static file server (Python, Node, etc.)

### Run the backend

```bash
go build -o chess1010 .
./chess1010 -addr :8080 -db chess.db
```

Available flags:

| Flag      | Default          | Description                        |
|-----------|------------------|------------------------------------|
| `-addr`   | `:8080`          | Listen address                     |
| `-db`     | `chess.db`       | SQLite database file path          |
| `-static` | `./static`       | Directory containing `index.html`  |
| `-assets` | `./static/assets`| Directory containing piece images  |

The backend also serves the frontend — open `http://localhost:8080` directly.

## Docker Deployment

```bash
cp .env.example .env
# Edit .env if the frontend and backend are on different origins (see below)

docker compose up --build
```

| Service  | Default port | Description                         |
|----------|-------------|--------------------------------------|
| backend  | 3002        | Go API server                        |
| frontend | 3001        | Nginx serving the SPA                |

The frontend proxies `/api/*` requests to the backend container, so no CORS configuration is needed in the default same-origin setup.

### Environment Variables

Set in `.env` (copy from `.env.example`):

| Variable   | Default | Description                                                                 |
|------------|---------|-----------------------------------------------------------------------------|
| `BASE_URL` | *(empty)* | Origin of the API server. Leave empty when nginx proxies `/api/*` to backend on the same host. Set to e.g. `https://api.example.com` for cross-origin deployments. |
| `API_PATH` | `/api`  | Path prefix for all API routes. Must match the Go server routing.           |

## API Reference

All endpoints are under `/api/`. JSON body for `POST` requests; query parameters where noted.

### Create a game

```
POST /api/game/create
```

```json
{
  "player_name":  "Alice",
  "player_color": "w",        // "w" | "b" | "random"
  "time_control": 300         // seconds per player; 0 = unlimited
                              // allowed values: 0, 180, 300, 600, 900
}
```

Response:

```json
{ "game_id": "abc123", "token": "<20-char token>", "color": "w" }
```

### Join a game

```
POST /api/game/{id}/join
```

```json
{ "player_name": "Bob" }
```

Response:

```json
{ "token": "<20-char token>", "color": "b" }
```

### Get game info

```
GET /api/game/{id}/info
```

Response fields: `game_id`, `mode`, `is_full`, `game_over`, `resigned`, `time_control`, `player_names`.

### Get legal moves

```
GET /api/game/{id}/legal?row=<r>&col=<c>&token=<token>
```

Response:

```json
{ "squares": [[3,4], [4,4]] }
```

### Make a move

```
POST /api/game/{id}/move
```

```json
{
  "token":     "<player token>",
  "from":      [6, 4],
  "to":        [5, 4],
  "promotion": "Q"   // optional; required when pawn reaches back rank
}
```

Response: `{ "ok": true }`

### Resign

```
POST /api/game/{id}/resign
```

```json
{ "token": "<player token>" }
```

### Live state (SSE)

```
GET /api/game/{id}/events?token=<token>
```

Establishes a persistent SSE stream. Events:

| Event      | Data                              |
|------------|-----------------------------------|
| `state`    | Full board state, clocks, history |
| `resigned` | `{ "by": "resign"\|"timeout", "loser": "w"\|"b", "winner": "w"\|"b" }` |

The `state` event payload includes: `board`, `turn`, `status` (`playing`/`check`/`checkmate`/`stalemate`/`resigned`), `winner`, `your_color`, `history`, `time_control`, `white_time_ms`, `black_time_ms`, `turn_started_at`, `player_names`, `waiting`.

## Board Coordinate System

Squares are addressed as `[row, col]` with `[0, 0]` at the top-left (black's back rank). Row increases downward; column increases rightward.
