# Multi-stage build
FROM golang:1.23-alpine3.20 AS builder
WORKDIR /src
ENV CGO_ENABLED=0 GOOS=linux
RUN apk update && apk upgrade --no-cache && apk add --no-cache git ca-certificates && update-ca-certificates
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -trimpath -ldflags "-s -w" -o /out/nodetaintshandler ./main.go

FROM gcr.io/distroless/static:nonroot
WORKDIR /app
COPY --from=builder /out/nodetaintshandler /app/nodetaintshandler
USER nonroot
ENTRYPOINT ["/app/nodetaintshandler"]