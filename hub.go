package main

import (
	"fmt"
	"net/http"
	"sync"
)

// Hub manages SSE connections keyed by gameID → color.
// Each subscriber gets a channel of raw "event: …\ndata: …\n\n" strings.
type Hub struct {
	mu      sync.Mutex
	clients map[string]map[string]chan string // gameID → color → ch
}

func NewHub() *Hub {
	return &Hub{clients: make(map[string]map[string]chan string)}
}

func (h *Hub) subscribe(gameID, color string) chan string {
	ch := make(chan string, 8)
	h.mu.Lock()
	if h.clients[gameID] == nil {
		h.clients[gameID] = make(map[string]chan string)
	}
	h.clients[gameID][color] = ch
	h.mu.Unlock()
	return ch
}

func (h *Hub) unsubscribe(gameID, color string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if m := h.clients[gameID]; m != nil {
		delete(m, color)
		if len(m) == 0 {
			delete(h.clients, gameID)
		}
	}
}

// Broadcast sends an SSE event to every subscriber in a game.
func (h *Hub) Broadcast(gameID, event, data string) {
	msg := fmt.Sprintf("event: %s\ndata: %s\n\n", event, data)
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, ch := range h.clients[gameID] {
		select {
		case ch <- msg:
		default: // drop if the client is too slow; it can re-poll
		}
	}
}

// Send sends an SSE event to a single subscriber (e.g. legal-moves response).
func (h *Hub) Send(gameID, color, event, data string) {
	msg := fmt.Sprintf("event: %s\ndata: %s\n\n", event, data)
	h.mu.Lock()
	ch := h.clients[gameID][color]
	h.mu.Unlock()
	if ch != nil {
		select {
		case ch <- msg:
		default:
		}
	}
}

// ServeSSE runs the blocking SSE loop for one client.
func (h *Hub) ServeSSE(w http.ResponseWriter, r *http.Request, gameID, color string) {
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "SSE not supported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	ch := h.subscribe(gameID, color)
	defer h.unsubscribe(gameID, color)

	for {
		select {
		case msg := <-ch:
			fmt.Fprint(w, msg)
			fl.Flush()
		case <-r.Context().Done():
			return
		}
	}
}
