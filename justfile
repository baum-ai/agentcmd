# Run `just` to list available commands.
default:
    @just --list

# Build all packages.
build:
    CGO_ENABLED=0 go build ./...

# Build the agentcmd CLI binary into ./bin.
install:
    go build -o bin/agentcmd ./cmd/agentcmd

# Run the full test suite.
test:
    go test ./...

# Run tests with the race detector (matches CI's `race` job).
test-race:
    go test -race -short ./...

# Vet, build and test under CGO_ENABLED=0 (matches CI's `pure-go` job).
ci: vet build test fmt-check check-deps

# Run go vet.
vet:
    go vet ./...

# Fail if the shell-only packages link the tree-sitter runtime (matches CI).
check-deps:
    test -z "$(go list -deps . ./rules/standard ./native/standard ./adapters ./stream | grep gotreesitter)"

# Format all Go source files in place.
fmt:
    gofmt -w .

# Fail if any file is not gofmt-formatted (matches CI's `pure-go` job).
fmt-check:
    test -z "$(gofmt -l .)"

# Run short fuzz smoke tests (matches CI's `fuzz-smoke` job).
fuzz-smoke:
    go test . -run xxx -fuzz FuzzAnalyzeShell -fuzztime 60s
    go test ./stream -run xxx -fuzz FuzzProcess -fuzztime 30s
    go test ./adapters -run xxx -fuzz FuzzDecode -fuzztime 30s

# Regenerate docs/rules.md from the standard rule set.
gen-rules-doc:
    go test ./rules/standard -update

# Regenerate the corpus distribution report.
gen-corpus-report:
    go test ./internal/corpus -update

# Regenerate the scorecard.
gen-scorecard:
    go test . -run TestScorecard -update

# Regenerate golden shell-analysis outputs under testdata/golden.
gen-golden:
    go test . -update

# Regenerate all generated docs and fixtures.
gen-all: gen-rules-doc gen-corpus-report gen-scorecard gen-golden

# Remove build artifacts.
clean:
    rm -rf bin/agentcmd
