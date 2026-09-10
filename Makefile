BINARY := exe
UNAME_S := $(shell uname -s)

.PHONY: build cross install clean

build:
	CGO_ENABLED=1 go build -o $(BINARY) ./cmd/exe

ifeq ($(UNAME_S),Linux)
	CGO_ENABLED=0 go build -o exe-net-helper ./cmd/exe-net-helper
endif

ifeq ($(UNAME_S),Darwin)
	codesign --entitlements vz.entitlements --force -s - $(BINARY)
endif

# Static Linux binaries for another machine — a NAS, a container, a box
# without Go. No cgo, so they run on any glibc or musl userland.
cross:
	mkdir -p dist
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o dist/exe-linux-amd64 ./cmd/exe
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o dist/exe-net-helper-linux-amd64 ./cmd/exe-net-helper
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o dist/exe-linux-arm64 ./cmd/exe
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o dist/exe-net-helper-linux-arm64 ./cmd/exe-net-helper

install: build
	mkdir -p $(HOME)/bin
	cp $(BINARY) $(HOME)/bin/$(BINARY)

clean:
	rm -rf $(BINARY) exe-net-helper dist
