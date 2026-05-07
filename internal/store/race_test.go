//go:build race

package store_test

// raceEnabled is true when the test binary was compiled with -race.
// Used by performance-proxy tests that need a more generous deadline under
// the race detector (which adds 5–20× overhead due to shadow memory).
const raceEnabled = true
