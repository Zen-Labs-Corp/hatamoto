module example.com/hello

go 1.26

require hatamoto.dev/sdk v0.8.0

require github.com/extism/go-pdk v1.1.3 // indirect

replace hatamoto.dev/sdk => ../../../backend/pkg/sdk
