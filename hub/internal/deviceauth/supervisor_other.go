//go:build !linux

package deviceauth

import "context"

func (e *WGExecutor) runSupervised(context.Context, ...string) ([]byte, error) {
	return nil, ErrSupervision
}
func RunExecutionSupervisor([]string) int { return 1 }
