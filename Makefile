# filename: Makefile
BINARY := choir

.PHONY: build run dry tidy clean

build:
	go build -o $(BINARY) ./cmd/choir

run: build
	./$(BINARY) -config choir.yaml

dry: build
	./$(BINARY) -config choir.yaml -dry-run

tidy:
	go mod tidy

clean:
	rm -f $(BINARY)
