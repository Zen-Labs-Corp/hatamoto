# Build and pack the plugin. Needs Go 1.27 or newer (1.26 once you move to SDK v0.12.0) and, to pack, the hatamoto binary.
build:
	rm -rf build
	GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o build/plugin.wasm .
	cp plugin.json build/plugin.json
	for d in migrations ui assets; do if [ -d $$d ]; then cp -r $$d build/$$d; fi; done

# Signed package. HATAMOTO_SIGNING_KEY (or --key) is a key from `hatamoto keys generate`.
pack: build
	hatamoto plugin pack ./build --out my-plugin.hmp

vet:
	GOOS=wasip1 GOARCH=wasm go vet ./...

clean:
	rm -rf build *.hmp

.PHONY: build pack vet clean
