FROM golang:1.25.4 AS builder

WORKDIR /app

# Cache dependencies first.
COPY go.mod go.sum ./
RUN go mod download

# Build static Linux binary.
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o chess1010 .

FROM gcr.io/distroless/static-debian12:nonroot

WORKDIR /app

COPY --from=builder /app/chess1010 /app/chess1010

EXPOSE 3002

ENTRYPOINT ["/app/chess1010"]
CMD ["-addr", ":3002", "-db", "/data/chess.db"]
