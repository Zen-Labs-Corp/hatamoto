//go:build race

package pluginhost

// raceEnabled is true under -race, where timings are not comparable.
const raceEnabled = true
