.PHONY: build build-linux build-linux-arm64 build-all clean run deps update

# Binary name
BINARY_NAME=deployer-agent

# Build for current platform
build:
	go build -o $(BINARY_NAME) .

# Build with optimizations (smaller binary)
build-release:
	go build -ldflags="-s -w" -o $(BINARY_NAME) .

# Build for Linux AMD64
build-linux:
	GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o $(BINARY_NAME)-linux-amd64 .

# Build for Linux ARM64
build-linux-arm64:
	GOOS=linux GOARCH=arm64 go build -ldflags="-s -w" -o $(BINARY_NAME)-linux-arm64 .

# Build for all platforms
build-all: build-release build-linux build-linux-arm64

# Download dependencies
deps:
	go mod download
	go mod tidy

# Run the application
run:
	go run . -config config.yaml

# Clean build artifacts
clean:
	rm -f $(BINARY_NAME)
	rm -f $(BINARY_NAME)-linux-amd64
	rm -f $(BINARY_NAME)-linux-arm64

# Update project: pull latest changes, rebuild, and restart service
update: 
	@echo "🔄 Updating deployer-agent..."
	@echo ""
	@echo "📥 Pulling latest changes from git..."
	git pull
	@echo "✅ Git pull completed"
	@echo ""
	@echo "🔨 Building project for current platform..."
	$(MAKE) build
	@echo "✅ Build completed"
	@echo ""
	@echo "🔄 Restarting deployer-agent service..."
	sudo systemctl restart deployer-agent
	@echo "✅ Service restarted"
	@echo ""
	@echo "✨ Update completed successfully!"

# Install to /opt/deployer-agent (requires sudo)
install: build-release
	sudo mkdir -p /opt/deployer-agent
	sudo cp $(BINARY_NAME) /opt/deployer-agent/
	sudo cp config.example.yaml /opt/deployer-agent/
	@echo "Installed to /opt/deployer-agent"
	@echo "Copy config.example.yaml to config.yaml and edit as needed"
