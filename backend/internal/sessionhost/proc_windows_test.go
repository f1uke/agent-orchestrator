//go:build windows

package sessionhost

// The shutdown test starts no child on Windows, where Process.Kill ends only
// claude itself and there is no process group to check.
func alive(int) bool { return false }

func killIfAlive(int) {}
