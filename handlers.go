package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ── Handler ───────────────────────────────────────────────────────────────────

type Handler struct {
	store *Store
	hub   *Hub
	mu    sync.Mutex // serialises writes per-server (SQLite is single-writer anyway)
}

func NewHandler(store *Store, hub *Hub) *Handler {
	return &Handler{store: store, hub: hub}
}

// ── Routing ───────────────────────────────────────────────────────────────────

func (h *Handler) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/game/create",      h.createGame)
	mux.HandleFunc("POST /api/game/{id}/join",   h.joinGame)
	mux.HandleFunc("GET  /api/game/{id}/info",   h.gameInfo)
	mux.HandleFunc("GET  /api/game/{id}/legal",  h.legalMoves)
	mux.HandleFunc("POST /api/game/{id}/move",   h.makeMove)
	mux.HandleFunc("POST /api/game/{id}/resign", h.resign)
	mux.HandleFunc("GET  /api/game/{id}/events", h.sseEvents)
	return cors(mux)
}

// ── POST /api/game/create ─────────────────────────────────────────────────────

type createReq struct {
	PlayerName  string `json:"player_name"`  // display name
	PlayerColor string `json:"player_color"` // "w" | "b" | "random"
	TimeControl int64  `json:"time_control"` // seconds per player; 0 = unlimited
}

func (h *Handler) createGame(w http.ResponseWriter, r *http.Request) {
	var req createReq
	req.PlayerColor = White
	_ = json.NewDecoder(r.Body).Decode(&req)

	// Resolve random side
	switch req.PlayerColor {
	case "random":
		n, _ := rand.Int(rand.Reader, big.NewInt(2))
		if n.Int64() == 0 {
			req.PlayerColor = White
		} else {
			req.PlayerColor = Black
		}
	case White, Black:
	default:
		req.PlayerColor = White
	}

	if req.PlayerName == "" {
		req.PlayerName = "Player"
	}
	// Clamp time control to sensible values
	allowed := map[int64]bool{0: true, 180: true, 300: true, 600: true, 900: true}
	if !allowed[req.TimeControl] {
		req.TimeControl = 0
	}

	id := newID(6)
	token := newID(20)

	if err := h.store.CreateGame(id, "human", NewGame(), req.TimeControl); err != nil {
		jsonError(w, "failed to create game", http.StatusInternalServerError)
		return
	}
	if err := h.store.AddPlayer(id, req.PlayerColor, token, req.PlayerName); err != nil {
		jsonError(w, "failed to add player", http.StatusInternalServerError)
		return
	}

	jsonOK(w, map[string]any{
		"game_id": id,
		"token":   token,
		"color":   req.PlayerColor,
	})
}

// ── POST /api/game/{id}/join ──────────────────────────────────────────────────

type joinReq struct {
	PlayerName string `json:"player_name"`
}

func (h *Handler) joinGame(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	var req joinReq
	_ = json.NewDecoder(r.Body).Decode(&req)
	if req.PlayerName == "" {
		req.PlayerName = "Player"
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	rec, err := h.store.GetGame(id)
	if err != nil || rec == nil {
		jsonError(w, "game not found", http.StatusNotFound)
		return
	}

	players, err := h.store.GetPlayers(id)
	if err != nil {
		jsonError(w, "db error", http.StatusInternalServerError)
		return
	}
	if len(players) >= 2 {
		jsonError(w, "game is full", http.StatusConflict)
		return
	}

	color := White
	if _, taken := players[White]; taken {
		color = Black
	}

	token := newID(20)
	if err := h.store.AddPlayer(id, color, token, req.PlayerName); err != nil {
		jsonError(w, "failed to join", http.StatusInternalServerError)
		return
	}

	// Start the clock now that both players are present
	nowMs := time.Now().UnixMilli()
	_ = h.store.StartClock(id, nowMs)
	rec.TurnStartedAt = nowMs

	// Notify the waiting player with updated state
	players[color] = PlayerInfo{Token: token, Name: req.PlayerName}
	h.pushAll(id, rec, players)

	jsonOK(w, map[string]any{"token": token, "color": color})
}

// ── GET /api/game/{id}/info ───────────────────────────────────────────────────

func (h *Handler) gameInfo(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	rec, err := h.store.GetGame(id)
	if err != nil || rec == nil {
		jsonError(w, "game not found", http.StatusNotFound)
		return
	}
	players, _ := h.store.GetPlayers(id)
	names := map[string]string{}
	for c, p := range players {
		names[c] = p.Name
	}
	jsonOK(w, map[string]any{
		"game_id":      id,
		"mode":         rec.Mode,
		"is_full":      len(players) >= 2,
		"game_over":    rec.State.IsCheckmate() || rec.State.IsStalemate() || rec.Resigned != "",
		"resigned":     rec.Resigned,
		"time_control": rec.TimeControl,
		"player_names": names,
	})
}

// ── GET /api/game/{id}/legal?row=r&col=c&token=t ─────────────────────────────

func (h *Handler) legalMoves(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	token := r.URL.Query().Get("token")

	row, err1 := strconv.Atoi(r.URL.Query().Get("row"))
	col, err2 := strconv.Atoi(r.URL.Query().Get("col"))
	if err1 != nil || err2 != nil {
		jsonError(w, "invalid row/col", http.StatusBadRequest)
		return
	}

	color, err := h.store.ColorForToken(id, token)
	if err != nil || color == "" {
		jsonError(w, "invalid token", http.StatusUnauthorized)
		return
	}

	rec, err := h.store.GetGame(id)
	if err != nil || rec == nil {
		jsonError(w, "game not found", http.StatusNotFound)
		return
	}

	moves := rec.State.LegalMovesFrom(row, col)
	squares := make([][2]int, len(moves))
	for i, m := range moves {
		squares[i] = m.End
	}
	jsonOK(w, map[string]any{"squares": squares})
}

// ── POST /api/game/{id}/move ──────────────────────────────────────────────────

type moveReq struct {
	Token     string `json:"token"`
	From      [2]int `json:"from"`
	To        [2]int `json:"to"`
	Promotion string `json:"promotion"`
}

func (h *Handler) makeMove(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	var req moveReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "bad request", http.StatusBadRequest)
		return
	}

	color, err := h.store.ColorForToken(id, req.Token)
	if err != nil || color == "" {
		jsonError(w, "invalid token", http.StatusUnauthorized)
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	rec, err := h.store.GetGame(id)
	if err != nil || rec == nil {
		jsonError(w, "game not found", http.StatusNotFound)
		return
	}
	if rec.State.Turn != color {
		jsonError(w, "not your turn", http.StatusBadRequest)
		return
	}
	if rec.Resigned != "" || rec.State.IsCheckmate() || rec.State.IsStalemate() {
		jsonError(w, "game is over", http.StatusBadRequest)
		return
	}

	// ── Clock: deduct elapsed time ─────────────────────────────────────────
	nowMs := time.Now().UnixMilli()
	whiteMs := rec.WhiteTimeMs
	blackMs := rec.BlackTimeMs

	if rec.TimeControl > 0 && rec.TurnStartedAt > 0 {
		elapsed := nowMs - rec.TurnStartedAt
		if color == White {
			whiteMs -= elapsed
		} else {
			blackMs -= elapsed
		}

		if whiteMs <= 0 || blackMs <= 0 {
			// Timeout: the mover ran out of time
			loser := color
			winner := Black
			if loser == Black {
				winner = White
			}
			_ = h.store.SetTimeout(id, loser)
			players, _ := h.store.GetPlayers(id)
			payload, _ := json.Marshal(map[string]string{"by": "timeout", "loser": loser, "winner": winner})
			h.hub.Broadcast(id, "resigned", string(payload))
			// Also push updated state so clocks show zero
			if color == White {
				whiteMs = 0
			} else {
				blackMs = 0
			}
			rec.WhiteTimeMs = whiteMs
			rec.BlackTimeMs = blackMs
			rec.Resigned = loser
			h.pushAll(id, rec, players)
			jsonOK(w, map[string]bool{"ok": true})
			return
		}
	}

	// ── Apply move ────────────────────────────────────────────────────────
	m := Move{Start: req.From, End: req.To, Promotion: req.Promotion}
	if !rec.State.MakeMove(m) {
		jsonError(w, "illegal move", http.StatusBadRequest)
		return
	}

	entry := HistoryEntry{From: req.From, To: req.To, Promotion: req.Promotion}
	if err := h.store.SaveMove(id, rec.State, entry, whiteMs, blackMs, nowMs); err != nil {
		jsonError(w, "failed to save", http.StatusInternalServerError)
		return
	}

	rec.WhiteTimeMs = whiteMs
	rec.BlackTimeMs = blackMs
	rec.TurnStartedAt = nowMs
	rec.History = append(rec.History, entry)

	players, _ := h.store.GetPlayers(id)
	h.pushAll(id, rec, players)

	jsonOK(w, map[string]bool{"ok": true})
}

// ── POST /api/game/{id}/resign ────────────────────────────────────────────────

type resignReq struct{ Token string `json:"token"` }

func (h *Handler) resign(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	var req resignReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "bad request", http.StatusBadRequest)
		return
	}

	color, err := h.store.ColorForToken(id, req.Token)
	if err != nil || color == "" {
		jsonError(w, "invalid token", http.StatusUnauthorized)
		return
	}

	if err := h.store.SetResigned(id, color); err != nil {
		jsonError(w, "db error", http.StatusInternalServerError)
		return
	}

	winner := Black
	if color == Black {
		winner = White
	}
	payload, _ := json.Marshal(map[string]string{"by": "resign", "loser": color, "winner": winner})
	h.hub.Broadcast(id, "resigned", string(payload))

	jsonOK(w, map[string]bool{"ok": true})
}

// ── GET /api/game/{id}/events?token=t  (SSE) ─────────────────────────────────

func (h *Handler) sseEvents(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	token := r.URL.Query().Get("token")

	color, err := h.store.ColorForToken(id, token)
	if err != nil || color == "" {
		http.Error(w, "invalid token", http.StatusUnauthorized)
		return
	}

	rec, err := h.store.GetGame(id)
	if err != nil || rec == nil {
		http.Error(w, "game not found", http.StatusNotFound)
		return
	}
	players, _ := h.store.GetPlayers(id)

	// Prime the SSE channel before blocking
	stateJSON := buildStateJSON(rec, players, color)
	go func() { h.hub.Send(id, color, "state", stateJSON) }()

	h.hub.ServeSSE(w, r, id, color)
}

// ── Broadcast helpers ─────────────────────────────────────────────────────────

// pushAll sends a personalised state event to every connected player.
func (h *Handler) pushAll(gameID string, rec *GameRecord, players map[string]PlayerInfo) {
	for color := range players {
		js := buildStateJSON(rec, players, color)
		h.hub.Send(gameID, color, "state", js)
	}
}

func buildStateJSON(rec *GameRecord, players map[string]PlayerInfo, forColor string) string {
	gs := rec.State

	status := "playing"
	var winner *string

	if rec.Resigned != "" {
		status = "resigned"
		w := Black
		if rec.Resigned == Black {
			w = White
		}
		winner = &w
	} else if gs.IsCheckmate() {
		status = "checkmate"
		w := Black
		if gs.Turn == Black {
			w = White
		}
		winner = &w
	} else if gs.IsStalemate() {
		status = "stalemate"
	} else if gs.IsInCheck(gs.Turn) {
		status = "check"
	}

	pnames := map[string]string{}
	for c, p := range players {
		pnames[c] = p.Name
	}

	history := rec.History
	if history == nil {
		history = []HistoryEntry{}
	}

	out, _ := json.Marshal(map[string]any{
		"board":           gs.Board,
		"turn":            gs.Turn,
		"status":          status,
		"winner":          winner,
		"waiting":         len(players) < 2,
		"your_color":      forColor,
		"history":         history,
		"time_control":    rec.TimeControl,
		"white_time_ms":   rec.WhiteTimeMs,
		"black_time_ms":   rec.BlackTimeMs,
		"turn_started_at": rec.TurnStartedAt,
		"player_names":    pnames,
	})
	return string(out)
}

// ── Utilities ─────────────────────────────────────────────────────────────────

func newID(bytes int) string {
	b := make([]byte, bytes)
	rand.Read(b)
	return hex.EncodeToString(b)[:bytes*2/3+1]
}

func jsonOK(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func jsonError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if strings.ToUpper(r.Method) == "OPTIONS" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
