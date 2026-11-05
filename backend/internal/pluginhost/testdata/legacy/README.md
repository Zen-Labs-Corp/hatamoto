# Frozen legacy guest

`legacy.wasm` stands for a plugin compiled before the SDK moved to JSON v2. It
is `main.go` linked against the SDK as of commit `dec7b490` (`hatamoto.dev/sdk`
0.18.0, `encoding/json` v1) and built with the Go 1.26.6 toolchain, whose
`encoding/json` is the real v1 encoder (Go 1.27 backs it with v2). Build it in
a scratch worktree of that commit, with `go.mod` set to `go 1.26` and the
`replace` pointing at the worktree's `backend/pkg/sdk`:

    GOTOOLCHAIN=go1.26.6 GOFLAGS=-mod=mod GOPROXY=off GOWORK=off \
    GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared \
        -ldflags='-s -w' -trimpath -o legacy.wasm .

`strings legacy.wasm | grep -m1 go1.26` shows the toolchain.

It is built once and committed on purpose, so `legacy_plugin_test.go` (test
`TestLegacyWasmLoadsAndRuns`) proves the current host still reads and answers
the bytes an old guest writes. Do not add a target that rebuilds it. It is
about 5.9 MB because the SDK links `net/http`.
