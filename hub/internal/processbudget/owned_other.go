//go:build !linux && !windows

package processbudget

import (
	"context"
	"errors"
	"time"
)

func SupervisorMain([]string) (int, bool) { return 0, false }
func runOwned(context.Context, string, []string, *capture, time.Duration) (bool, bool, error) {
	return false, true, errors.New("unsupported local supervision")
}
