# Terminal 1 — Go API

cd goserver
go build -o chess1010 .
./chess1010 -addr :8080 -db chess.db

# Terminal 2 — serve the HTML (any static server)

cd goserver/static
python3 -m http.server 3000

# open http://localhost:3000
