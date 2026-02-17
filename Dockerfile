FROM golang:1.22-alpine AS builder

RUN apk add --no-cache gcc musl-dev git

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=1 GOOS=linux go build -o /bot ./cmd/bot/

FROM alpine:3.19

RUN apk add --no-cache ca-certificates tzdata

WORKDIR /app

COPY --from=builder /bot /app/bot
COPY templates/ /app/templates/
COPY static/ /app/static/
COPY config.example.yaml /app/config.example.yaml

RUN mkdir -p /app/data

EXPOSE 8080

ENTRYPOINT ["/app/bot"]
CMD ["-config", "/app/config.yaml", "-db", "/app/data/rus-trader.db"]
