default:
    @just --list

# Build the binary into ./bin
build:
    go build -o bin/mailotron ./cmd/mailotron

# Install to GOBIN
install:
    go install ./cmd/mailotron

# Run unit tests with the race detector
test:
    go test ./... -race -count=1

# Run end-to-end tests (requires Docker; spins up GreenMail + Mailpit)
e2e:
    go test -tags e2e ./test/e2e/ -count=1 -timeout 600s

# Static checks
vet:
    go vet ./...

# Validate the GoReleaser config
release-check:
    goreleaser check

# Cross-compile a local snapshot release (no publish)
snapshot:
    goreleaser release --snapshot --clean

# Print the baked-in agent guide
guide:
    go run ./cmd/mailotron guide
