// Package unpixel goroutine-leak gate: goleak checks that no goroutine
// outlives the test binary. Runs automatically via TestMain — no per-test
// annotation needed.
package unpixel

import (
	"testing"

	"go.uber.org/goleak"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}
