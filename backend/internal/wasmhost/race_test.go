//go:build race

package wasmhost

// raceEnabled is true under -race, where timings and memory are not
// comparable with a normal build.
const raceEnabled = true
