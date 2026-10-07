//go:build !darwin && !linux

package processtree

import "errors"

// Parent returns the parent pid of pid. It is not implemented here, so callers
// treat the answer as unknown.
func Parent(pid int) (int, error) {
	return 0, errors.ErrUnsupported
}
