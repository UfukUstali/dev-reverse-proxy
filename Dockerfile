FROM golang:1.23-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY server/ ./server/
RUN CGO_ENABLED=0 GOOS=linux go build -o /app/server-bin ./server/

FROM alpine:latest

RUN apk --no-cache add ca-certificates

WORKDIR /root/

COPY --from=builder /app/server-bin .

EXPOSE 8080

ENV CONFIG_DIR=/config

CMD ["./server-bin"]
