// Package processtree reads an operating-system process's place in the
// process tree.
package processtree

import "errors"

// Parent returns the parent pid of pid.
func Parent(pid int) (int, error) {
	return 0, errors.ErrUnsupported
}
