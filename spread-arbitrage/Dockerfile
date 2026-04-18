# Build stage
FROM golang:1.26-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o spread-arbitrage .

# Runtime — API only, no frontend
FROM alpine:3.22
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=builder /app/spread-arbitrage .
RUN mkdir -p logs
EXPOSE 18527
CMD ["./spread-arbitrage"]
