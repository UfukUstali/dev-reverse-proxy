FROM golang:1.26.5-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -o /app/server-bin ./server

FROM alpine:3.23
RUN apk --no-cache add ca-certificates
COPY --from=builder /app/server-bin /usr/local/bin/devrp-gateway
ENV CONFIG_DIR=/config
ENTRYPOINT ["devrp-gateway"]
