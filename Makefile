.PHONY: all test

all: test

test:
	go test -race -cover ./...
