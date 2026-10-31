VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: build test vet cross clean
build:
	CGO_ENABLED=0 go build -trimpath -ldflags="$(LDFLAGS)" -o deadzone ./cmd/deadzone
test:
	go test -count=1 ./...
vet:
	go vet ./...
cross:
	mkdir -p dist
	CGO_ENABLED=0 GOOS=linux  GOARCH=amd64 go build -trimpath -ldflags="$(LDFLAGS)" -o dist/deadzone-linux-amd64  ./cmd/deadzone
	CGO_ENABLED=0 GOOS=linux  GOARCH=arm64 go build -trimpath -ldflags="$(LDFLAGS)" -o dist/deadzone-linux-arm64  ./cmd/deadzone
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags="$(LDFLAGS)" -o dist/deadzone-darwin-arm64 ./cmd/deadzone
clean:
	rm -rf dist deadzone
