.PHONY: all tidy test build-darwin build-linux-docker clean

PLUGIN_NAME = kiro4cpa

all: tidy test build-darwin

tidy:
	go mod tidy

test:
	go test -v ./...

build-darwin:
	CGO_ENABLED=1 go build -trimpath -buildmode=c-shared -o bin/darwin/arm64/$(PLUGIN_NAME).dylib .

build-linux-docker:
	docker run --rm -v "$$(pwd)":/app -v "$$(go env GOMODCACHE)":/go/pkg/mod -w /app golang:1.26-bookworm \
		sh -c "apt-get update -qq && apt-get install -y -qq gcc-x86-64-linux-gnu libc6-dev-amd64-cross && \
		CGO_ENABLED=1 GOOS=linux GOARCH=amd64 CC=x86_64-linux-gnu-gcc go build -trimpath -buildmode=c-shared -o bin/linux/amd64/$(PLUGIN_NAME).so ."

clean:
	rm -rf bin/
