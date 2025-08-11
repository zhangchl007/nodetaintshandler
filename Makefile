# Variables
BINARY_NAME=nodetaintshandler
VERSION=$(shell git describe --tags --always --dirty)
BUILD_TIME=$(shell date -u '+%Y-%m-%d_%H:%M:%S')
DOCKER_REPO=zhangchl007
DOCKER_IMAGE=$(DOCKER_REPO)/$(BINARY_NAME)
DOCKER_TAG?=latest

# Go related variables
GO=go
GOFLAGS=-v
LDFLAGS=-X main.version=$(VERSION) -X main.buildTime=$(BUILD_TIME)

.PHONY: all build clean test docker-build docker-push fmt lint deploy help

all: build

# Build the binary
build:
    $(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BINARY_NAME) .

# Run tests
test:
    $(GO) test ./... -coverprofile=coverage.out

# Clean build artifacts
clean:
    rm -f $(BINARY_NAME)
    rm -f coverage.out

# Build Docker image
docker-build: build
    docker build -t $(DOCKER_IMAGE):$(DOCKER_TAG) .

# Push Docker image
docker-push: docker-build
    docker push $(DOCKER_IMAGE):$(DOCKER_TAG)

# Format Go code
fmt:
    $(GO) fmt ./...

# Lint the code
lint:
    golint ./...
    go vet ./...

# Deploy to Kubernetes
deploy:
    kubectl apply -f deployment.yaml

# Run the application locally (for development)
run: build
    ./$(BINARY_NAME)

# Show help
help:
    @echo "Make targets:"
    @echo "  build        - Build the binary"
    @echo "  test         - Run tests"
    @echo "  clean        - Remove build artifacts"
    @echo "  docker-build - Build Docker image"
    @echo "  docker-push  - Build and push Docker image"
    @echo "  fmt          - Format Go code"
    @echo "  lint         - Lint the code"
    @echo "  deploy       - Deploy to Kubernetes"
    @echo "  run          - Run locally for development"