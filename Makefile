# Compila tudo via Docker — não precisa de Go instalado na máquina.
IMAGE := golang:1.25-alpine
GO    := docker run --rm -v "$(CURDIR)":/src -w /src -e CGO_ENABLED=0 $(IMAGE)

.PHONY: dist tidy server clean

## dist: binários do CLI (Mac Intel/ARM, Windows) + servidor Linux em ./dist
dist:
	$(GO) sh -c '\
		GOOS=darwin  GOARCH=arm64 go build -trimpath -ldflags "-s -w" -o dist/lgrok-darwin-arm64      ./cmd/lgrok  && \
		GOOS=darwin  GOARCH=amd64 go build -trimpath -ldflags "-s -w" -o dist/lgrok-darwin-amd64      ./cmd/lgrok  && \
		GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "-s -w" -o dist/lgrok-windows-amd64.exe ./cmd/lgrok  && \
		GOOS=linux   GOARCH=amd64 go build -trimpath -ldflags "-s -w" -o dist/lgrokd-linux-amd64      ./cmd/lgrokd'

## tidy: atualiza go.mod/go.sum
tidy:
	$(GO) go mod tidy

## server: sobe o servidor local de teste
server:
	docker compose up -d --build

clean:
	rm -rf dist
