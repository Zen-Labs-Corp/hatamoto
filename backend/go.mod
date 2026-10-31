module hatamoto.dev/core

go 1.27

toolchain go1.27.3

require (
	github.com/extism/go-sdk v1.7.1
	github.com/google/uuid v1.6.0
	github.com/jackc/pgx/v5 v5.11.0
	github.com/microcosm-cc/bluemonday v1.0.27
	github.com/modelcontextprotocol/go-sdk v1.8.0
	github.com/oschwald/maxminddb-golang v1.13.1
	github.com/pressly/goose/v3 v3.29.0
	github.com/santhosh-tekuri/jsonschema/v5 v5.3.1
	github.com/tetratelabs/wazero v1.9.0
	golang.org/x/crypto v0.58.0
	golang.org/x/image v0.47.0
	golang.org/x/net v0.62.0
	golang.org/x/text v0.43.0
	golang.org/x/time v0.16.0
	hatamoto.dev/sdk v0.0.0-00010101000000-000000000000
)

require (
	github.com/aymerick/douceur v0.2.0 // indirect
	github.com/dylibso/observe-sdk/go v0.0.0-20240828172851-9145d8ad07e1 // indirect
	github.com/gobwas/glob v1.0.0 // indirect
	github.com/google/jsonschema-go v0.4.3 // indirect
	github.com/gorilla/css v1.0.1 // indirect
	github.com/ianlancetaylor/demangle v0.0.0-20261012173340-cbb8aaea4290 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.3 // indirect
	github.com/mfridman/interpolate v0.0.2 // indirect
	github.com/segmentio/asm v1.2.1 // indirect
	github.com/segmentio/encoding v0.5.4 // indirect
	github.com/sethvargo/go-retry v0.5.0 // indirect
	github.com/tetratelabs/wabin v0.0.0-20230304001439-f6f874872834 // indirect
	github.com/yosida95/uritemplate/v3 v3.0.2 // indirect
	go.opentelemetry.io/proto/otlp v1.11.1 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	golang.org/x/oauth2 v0.37.0 // indirect
	golang.org/x/sync v0.24.0 // indirect
	golang.org/x/sys v0.49.0 // indirect
	google.golang.org/protobuf v1.36.12 // indirect
)

replace hatamoto.dev/sdk => ./pkg/sdk
