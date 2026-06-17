# Multi-stage Go build
FROM golang:1.23-alpine AS builder

WORKDIR /app

# Install git for go modules (if needed)
RUN apk add --no-cache git

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /rate-limiter .

# Final minimal image
FROM alpine:3.20

RUN apk --no-cache add ca-certificates tzdata

WORKDIR /app
COPY --from=builder /rate-limiter /app/rate-limiter

EXPOSE 8080

ENTRYPOINT ["/app/rate-limiter"]
CMD ["-port", "8080"]
