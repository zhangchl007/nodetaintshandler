# Variables
BINARY_NAME=nodetaintshandler
VERSION=$(shell git describe --tags --always --dirty)
BUILD_TIME=$(shell date -u '+%Y-%m-%d_%H:%M:%S')
DOCKER_REPO=zhangchl007
DOCKER_IMAGE=$(DOCKER_REPO)/$(BINARY_NAME)
# or:    make docker-push DOCKER_TAG=v1.3

# Go related variables
GO=go
GOFLAGS=-v
LDFLAGS=-X main.version=$(VERSION) -X main.buildTime=$(BUILD_TIME)

.PHONY: all build clean test docker-build docker-push fmt lint deploy help run

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
	@test -n "$(DOCKER_TAG)" || (echo "ERROR: set DOCKER_TAG (e.g. make docker-build DOCKER_TAG=v1.3)" >&2; exit 1)
	docker build -t $(DOCKER_IMAGE):$(DOCKER_TAG) .

# Push Docker image
docker-push: docker-build
	@test -n "$(DOCKER_TAG)" || (echo "ERROR: set DOCKER_TAG (e.g. make docker-push DOCKER_TAG=v1.3)" >&2; exit 1)
	docker push $(DOCKER_IMAGE):$(DOCKER_TAG)

# Format Go code
fmt:
	$(GO) fmt ./...

# Lint the code
lint:
	go vet ./...

# Deploy to Kubernetes
deploy:
	kubectl apply -f deploy/deployment.yaml

# Run locally (needs kubeconfig)
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
