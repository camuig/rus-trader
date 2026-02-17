.PHONY: build run close-all close-all-dry docker docker-down clean

# Build all binaries
build:
	CGO_ENABLED=1 go build -o bin/bot ./cmd/bot/
	CGO_ENABLED=1 go build -o bin/closeall ./cmd/closeall/

# Run the bot
run: build
	mkdir -p data
	./bin/bot -config config.yaml -db data/rus-trader.db

# Close all open positions
close-all: build
	./bin/closeall -config config.yaml

# Dry run — show positions without closing
close-all-dry: build
	./bin/closeall -config config.yaml -dry-run

# Docker
docker:
	docker-compose up --build -d

docker-down:
	docker-compose down

# Clean build artifacts
clean:
	rm -rf bin/
