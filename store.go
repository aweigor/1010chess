package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// ── Schema ────────────────────────────────────────────────────────────────────

const schema = `
CREATE TABLE IF NOT EXISTS games (
    id              TEXT    PRIMARY KEY,
    state_json      TEXT    NOT NULL,
    history_json    TEXT    NOT NULL DEFAULT '[]',
    mode            TEXT    NOT NULL DEFAULT 'human',
    resigned        TEXT,
    time_control    INTEGER NOT NULL DEFAULT 0,
    white_time_ms   INTEGER NOT NULL DEFAULT 0,
    black_time_ms   INTEGER NOT NULL DEFAULT 0,
    turn_started_at INTEGER NOT NULL DEFAULT 0,
    created_at      INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS players (
    game_id TEXT NOT NULL,
    color   TEXT NOT NULL,
    token   TEXT NOT NULL,
    name    TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (game_id, color)
);
`

// Applied after CREATE to handle existing databases missing new columns.
var migrations = []string{
	`ALTER TABLE games ADD COLUMN history_json    TEXT    NOT NULL DEFAULT '[]'`,
	`ALTER TABLE games ADD COLUMN time_control    INTEGER NOT NULL DEFAULT 0`,
	`ALTER TABLE games ADD COLUMN white_time_ms   INTEGER NOT NULL DEFAULT 0`,
	`ALTER TABLE games ADD COLUMN black_time_ms   INTEGER NOT NULL DEFAULT 0`,
	`ALTER TABLE games ADD COLUMN turn_started_at INTEGER NOT NULL DEFAULT 0`,
	`ALTER TABLE players ADD COLUMN name TEXT NOT NULL DEFAULT ''`,
}

// ── Public types ──────────────────────────────────────────────────────────────

type HistoryEntry struct {
	From      [2]int `json:"from"`
	To        [2]int `json:"to"`
	Promotion string `json:"promotion,omitempty"`
}

type PlayerInfo struct {
	Token string
	Name  string
}

type GameRecord struct {
	ID            string
	State         *GameState
	History       []HistoryEntry
	Mode          string
	Resigned      string
	TimeControl   int64 // seconds; 0 = unlimited
	WhiteTimeMs   int64 // remaining milliseconds
	BlackTimeMs   int64
	TurnStartedAt int64 // unix-ms when current player's clock started; 0 = not yet
	CreatedAt     time.Time
}

// ── Store ─────────────────────────────────────────────────────────────────────

type Store struct{ db *sql.DB }

func NewStore(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		return nil, fmt.Errorf("create schema: %w", err)
	}
	for _, m := range migrations {
		db.Exec(m) // ignore "duplicate column" errors on existing dbs
	}
	return &Store{db: db}, nil
}

// ── Game CRUD ─────────────────────────────────────────────────────────────────

func (s *Store) CreateGame(id, mode string, state *GameState, timeControl int64) error {
	stateData, err := json.Marshal(state)
	if err != nil {
		return err
	}
	timeMs := timeControl * 1000
	_, err = s.db.Exec(
		`INSERT INTO games
		    (id, state_json, history_json, mode, time_control, white_time_ms, black_time_ms, created_at)
		 VALUES (?, ?, '[]', ?, ?, ?, ?, ?)`,
		id, string(stateData), mode, timeControl, timeMs, timeMs, time.Now().Unix(),
	)
	return err
}

func (s *Store) GetGame(id string) (*GameRecord, error) {
	row := s.db.QueryRow(`
		SELECT id, state_json, history_json, mode,
		       COALESCE(resigned,''),
		       time_control, white_time_ms, black_time_ms, turn_started_at,
		       created_at
		FROM games WHERE id = ?`, id)

	var rec GameRecord
	var stateJSON, histJSON string
	var ts int64
	err := row.Scan(
		&rec.ID, &stateJSON, &histJSON, &rec.Mode,
		&rec.Resigned,
		&rec.TimeControl, &rec.WhiteTimeMs, &rec.BlackTimeMs, &rec.TurnStartedAt,
		&ts,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	rec.CreatedAt = time.Unix(ts, 0)

	var gs GameState
	if err := json.Unmarshal([]byte(stateJSON), &gs); err != nil {
		return nil, err
	}
	rec.State = &gs

	if err := json.Unmarshal([]byte(histJSON), &rec.History); err != nil {
		rec.History = nil
	}
	return &rec, nil
}

// SaveMove atomically persists new state, appended history entry, and updated clocks.
func (s *Store) SaveMove(
	gameID string,
	state *GameState,
	entry HistoryEntry,
	whiteMs, blackMs, turnStartedAt int64,
) error {
	stateData, err := json.Marshal(state)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`
		UPDATE games
		SET state_json      = ?,
		    history_json    = json_insert(history_json, '$[#]', json(?)),
		    white_time_ms   = ?,
		    black_time_ms   = ?,
		    turn_started_at = ?
		WHERE id = ?`,
		string(stateData),
		mustJSON(entry),
		whiteMs, blackMs, turnStartedAt,
		gameID,
	)
	return err
}

func (s *Store) SetResigned(gameID, color string) error {
	_, err := s.db.Exec(`UPDATE games SET resigned = ? WHERE id = ?`, color, gameID)
	return err
}

// StartClock sets turn_started_at to now — called when the second player joins.
func (s *Store) StartClock(gameID string, nowMs int64) error {
	_, err := s.db.Exec(`UPDATE games SET turn_started_at = ? WHERE id = ?`, nowMs, gameID)
	return err
}

// SetTimeout marks a player as losing on time by setting resigned to the loser.
func (s *Store) SetTimeout(gameID, loser string) error {
	return s.SetResigned(gameID, loser)
}

// ── Player CRUD ───────────────────────────────────────────────────────────────

func (s *Store) AddPlayer(gameID, color, token, name string) error {
	_, err := s.db.Exec(
		`INSERT INTO players (game_id, color, token, name) VALUES (?, ?, ?, ?)`,
		gameID, color, token, name,
	)
	return err
}

// GetPlayers returns color → PlayerInfo for all players in a game.
func (s *Store) GetPlayers(gameID string) (map[string]PlayerInfo, error) {
	rows, err := s.db.Query(`SELECT color, token, name FROM players WHERE game_id = ?`, gameID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]PlayerInfo{}
	for rows.Next() {
		var color, token, name string
		if err := rows.Scan(&color, &token, &name); err != nil {
			return nil, err
		}
		out[color] = PlayerInfo{Token: token, Name: name}
	}
	return out, rows.Err()
}

// ColorForToken resolves a token to its color.
func (s *Store) ColorForToken(gameID, token string) (string, error) {
	row := s.db.QueryRow(
		`SELECT color FROM players WHERE game_id = ? AND token = ?`, gameID, token,
	)
	var color string
	if err := row.Scan(&color); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		return "", err
	}
	return color, nil
}

// ── Util ──────────────────────────────────────────────────────────────────────

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
