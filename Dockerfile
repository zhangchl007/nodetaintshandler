# Multi-stage build
FROM golang:1.24-alpine3.20 AS builder

WORKDIR /app

# Copy go mod and sum files
COPY go.mod go.sum ./
RUN go mod download

# Copy the source code
COPY . .

# Build the Go app
RUN CGO_ENABLED=0 GOOS=linux go build -o nodetaintshandler main.go

# Use a minimal image for running
# FROM gcr.io/distroless/base-debian12
FROM alpine:latest
RUN apk add --no-cache ca-certificates
WORKDIR /app

COPY --from=builder /app/nodetaintshandler .

EXPOSE 8080

ENTRYPOINT ["/app/nodetaintshandler"]