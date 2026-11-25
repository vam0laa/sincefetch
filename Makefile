PREFIX ?= $(HOME)/.local
VERSION ?= 0.1.0

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o sincefetch .

test:
	gofmt -l . && go vet ./... && go test ./...

install: build
	install -Dm755 sincefetch $(DESTDIR)$(PREFIX)/bin/sincefetch

uninstall:
	rm -f $(DESTDIR)$(PREFIX)/bin/sincefetch

.PHONY: build test install uninstall
