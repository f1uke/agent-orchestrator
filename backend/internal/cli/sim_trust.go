package cli

import (
	"fmt"
	"io"
	"time"
)

// simTrustClient mirrors controllers.SimTrustView: what AO did about root CAs
// on a device when it booted or claimed it.
type simTrustClient struct {
	Trusted []string                `json:"trusted,omitempty"`
	Failed  []simTrustFailureClient `json:"failed,omitempty"`
	At      time.Time               `json:"at"`
}

// simTrustFailureClient mirrors controllers.SimTrustFailureView.
type simTrustFailureClient struct {
	File   string `json:"file,omitempty"`
	Reason string `json:"reason"`
}

// writeSimTrust says which root CAs the device now trusts, and warns about the
// ones it could not be made to. Nothing at all is printed when there was
// nothing to trust - a CA file that is not on this Mac is skipped silently.
//
// The warning names the symptom on purpose. A device that does not trust the
// debugging proxy's CA fails every HTTPS call and leaves the app on its splash
// screen, which reads like an app or backend bug unless somebody was told.
func writeSimTrust(out io.Writer, trust *simTrustClient) error {
	if trust == nil {
		return nil
	}
	for _, file := range trust.Trusted {
		if _, err := fmt.Fprintf(out, "Trusted root CA: %s\n", file); err != nil {
			return err
		}
	}
	for _, f := range trust.Failed {
		what := "Warning: could not make this simulator trust its root CAs"
		if f.File != "" {
			what = "Warning: could not make this simulator trust " + f.File
		}
		if _, err := fmt.Fprintf(out, "%s - %s\n"+
			"HTTPS through this Mac's debugging proxy will fail on this device until it does.\n",
			what, endSentence(f.Reason)); err != nil {
			return err
		}
	}
	return nil
}
