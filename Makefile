.PHONY: build test vet
build:
	mkdir -p bin
	go build -o bin/xssh ./cmd/xssh
	go build -o bin/xweb ./cmd/xweb
test:
	go test -race ./...
	node --test extension/*.test.js
vet:
	go vet ./...
