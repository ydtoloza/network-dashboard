# Build stage
FROM golang:1.21-alpine AS builder
WORKDIR /app
COPY go.mod ./
# Copy main source code
COPY main.go ./
RUN go build -o network-dashboard .

# Final stage
FROM alpine:latest
WORKDIR /app

# Install vnstat and tzdata
RUN apk add --no-cache vnstat tzdata

# Copy the binary from builder
COPY --from=builder /app/network-dashboard .

# Copy the frontend files (assuming they will be in public/)
# We will create this folder next
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
