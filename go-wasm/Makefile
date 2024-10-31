build-wasm-with-go:
	GOOS=js GOARCH=wasm go build -o celformat.wasm github.com/elastic/celfmt/go-wasm

build-wasm-with-tinygo:
	GOOS=js GOARCH=wasm tinygo build -o celformat.wasm github.com/elastic/celfmt/go-wasm

test:
	@echo "no tests"