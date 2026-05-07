//go:build !race

package store_test

// raceEnabled is false when the test binary was compiled without -race.
const raceEnabled = false
