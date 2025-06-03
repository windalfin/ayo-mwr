# ---- Build stage ----
FROM golang:1.24 as builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .

# Build the Go app (assumes main.go is the entrypoint)
RUN CGO_ENABLED=1 GOOS=linux go build -o ayo-mwr main.go

# ---- Runtime stage ----
FROM debian:bookworm-slim
WORKDIR /app

# Install runtime dependencies (ffmpeg, sqlite3, serial tools)
RUN apt-get update && \
    apt-get install -y --no-install-recommends ffmpeg sqlite3 ca-certificates && \
    rm -rf /var/lib/apt/lists/*

# Copy binary and assets
COPY --from=builder /app/ayo-mwr ./ayo-mwr
COPY .env.template ./
COPY server.log ./
# Persistent data directories (do NOT copy from build context)
# Use Docker volumes to mount host folders for /app/videos and /app/recordings
RUN mkdir -p /app/recordings /app/videos

# Example usage:
# docker run --env-file .env -p 8080:8080 \
#   -v /host/path/to/videos:/app/videos \
#   -v /host/path/to/recordings:/app/recordings \
#   ayo-mwr

# Expose the default HTTP port (change if needed)
EXPOSE 8080

# Set environment variables (override at runtime as needed)
ENV ENV=production

# Start the Go app
CMD ["./ayo-mwr"]
