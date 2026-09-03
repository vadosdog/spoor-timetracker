# spoor — task runner. `just` with no arguments lists the recipes.

version := `git describe --tags --always --dirty 2>/dev/null || echo dev`
ldflags := "-s -w -X github.com/vadosdog/spoor-timetracker/internal/cli.Version=" + version

default:
    @just --list

# Build the binary into ./bin/spoor
build:
    go build -ldflags "{{ldflags}}" -o bin/spoor ./cmd/spoor

# Run the tests
test:
    go test ./...

# Run the tests with the race detector
test-race:
    go test -race ./...

vet:
    go vet ./...

fmt:
    gofmt -l -w .

# Fail if anything is unformatted
fmt-check:
    @test -z "$(gofmt -l .)" || { echo "not gofmt'd:"; gofmt -l .; exit 1; }

lint:
    golangci-lint run

# Everything CI runs, in the same order
ci: fmt-check vet lint test build

clean:
    rm -rf bin
