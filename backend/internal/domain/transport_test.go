package domain

import "testing"

func TestParseTransport(t *testing.T) {
	for in, want := range map[string]Transport{"": "", "terminal": TransportTerminal, "chat": TransportChat} {
		got, err := ParseTransport(in)
		if err != nil || got != want {
			t.Errorf("ParseTransport(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"Chat", "tui", " chat"} {
		if got, err := ParseTransport(in); err == nil {
			t.Errorf("ParseTransport(%q) = %q, nil; want an error", in, got)
		}
	}
}

func TestProjectConfigValidate_WorkerTransport(t *testing.T) {
	for _, ok := range []Transport{"", TransportTerminal, TransportChat} {
		if err := (ProjectConfig{WorkerTransport: ok}).Validate(); err != nil {
			t.Errorf("workerTransport %q: %v", ok, err)
		}
	}
	if err := (ProjectConfig{WorkerTransport: "pigeon"}).Validate(); err == nil {
		t.Error("workerTransport pigeon was accepted")
	}
}
