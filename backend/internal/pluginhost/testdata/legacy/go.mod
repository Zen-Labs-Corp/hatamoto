module example.com/legacy

go 1.26

require hatamoto.dev/sdk v0.6.0

require github.com/extism/go-pdk v1.1.3 // indirect

replace hatamoto.dev/sdk => ../../../../pkg/sdk
