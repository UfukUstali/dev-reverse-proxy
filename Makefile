.PHONY: build up down restart logs status clients

# Build and start the infrastructure
up:
	docker-compose up -d

# Build images
build:
	docker-compose build

# Stop the infrastructure
down:
	docker-compose down

# Restart everything
restart: down up

# View logs
logs:
	docker-compose logs -f

# Server logs only
server-logs:
	docker-compose logs -f proxy-server

# Traefik logs only
traefik-logs:
	docker-compose logs -f traefik

# Check status
status:
	curl http://localhost:8080/status

# List registered clients
clients:
	curl http://localhost:8080/clients

# Clean up
clean:
	docker-compose down -v
	docker system prune -f

# Development - run Go server locally
dev-server:
	go run ./server/main.go

# Build Go server
build-server:
	go build -o server-bin ./server/
