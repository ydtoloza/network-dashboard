# Build stage
FROM golang:1.21-alpine AS builder
WORKDIR /app
COPY go.mod ./
COPY main.go ./
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o network-dashboard .

# Final stage
FROM alpine:3.20
WORKDIR /app

# Install vnstat and tzdata
RUN apk add --no-cache vnstat tzdata

# Copy the binary from builder
COPY --from=builder /app/network-dashboard .

# Copy the frontend files
COPY public/ ./public/

# Copy entrypoint script
COPY entrypoint.sh .
RUN chmod +x entrypoint.sh

# Expose default port
EXPOSE 8080

# Environment variables
ENV PORT=8080
ENV INTERFACES=eth0

ENTRYPOINT ["./entrypoint.sh"]
