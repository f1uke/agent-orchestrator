package fingerprint

import "testing"

// The hook, the messenger and capture each see the same words with different
// line handling; all three must land on one fingerprint.
func TestOf_IgnoresHowWhitespaceArrived(t *testing.T) {
	a, na := Of("no, use the\nscript  ")
	b, nb := Of("  no,  use the script")
	if a != b || na != nb || na != len("no, use the script") {
		t.Errorf("Of differs: %s/%d vs %s/%d", a, na, b, nb)
	}
	if c, _ := Of("no, use the scripts"); c == a {
		t.Error("different words share a fingerprint")
	}
}
