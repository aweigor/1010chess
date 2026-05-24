package main

import (
	"flag"
	"log"
	"net/http"
	"path/filepath"
)

func main() {
	addr      := flag.String("addr",   ":8080",    "listen address")
	dbPath    := flag.String("db",     "chess.db", "SQLite database file")
	staticDir := flag.String("static", "./static", "directory containing index.html")
	assetsDir := flag.String("assets", "./static/assets", "directory containing piece images")
	flag.Parse()

	store, err := NewStore(*dbPath)
	if err != nil {
		log.Fatalf("store: %v", err)
	}

	hub     := NewHub()
	handler := NewHandler(store, hub)

	top := http.NewServeMux()
	top.Handle("/api/", handler.Routes())
	top.Handle("/assets/", http.StripPrefix("/assets/", http.FileServer(http.Dir(*assetsDir))))
	top.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, filepath.Join(*staticDir, "index.html"))
	})

	log.Printf("Chess 1010 listening on %s  (db: %s)", *addr, *dbPath)
	if err := http.ListenAndServe(*addr, top); err != nil {
		log.Fatal(err)
	}
}
