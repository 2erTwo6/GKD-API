.PHONY: all build build-web test clean run

all: build

build-web:
	cd web && npm install --registry=https://registry.npmmirror.com && npm run build

build: build-web
	go build -o gkd-api ./cmd/server

test:
	go test ./...

run: build
	./gkd-api

clean:
	rm -f gkd-api gkd-api.db
	rm -rf web/dist
