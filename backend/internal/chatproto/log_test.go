package chatproto

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func loadFixture(t *testing.T, name string) []Entry {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	entries, err := DecodeLog(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("DecodeLog(%s): %v", name, err)
	}
	return entries
}

func assertJSONEqual(t *testing.T, got, want []byte) {
	t.Helper()
	var g, w any
	if err := json.Unmarshal(got, &g); err != nil {
		t.Fatalf("got is not JSON: %v\n%s", err, got)
	}
	if err := json.Unmarshal(want, &w); err != nil {
		t.Fatalf("want is not JSON: %v\n%s", err, want)
	}
	if !reflect.DeepEqual(g, w) {
		t.Errorf("JSON differs\n got: %s\nwant: %s", got, want)
	}
}

func TestDecodeLogFixtures(t *testing.T) {
	for name, wantLen := range map[string]int{"first_session.jsonl": 67, "resumed_session.jsonl": 91} {
		entries := loadFixture(t, name)
		if len(entries) != wantLen {
			t.Errorf("%s: %d entries, want %d", name, len(entries), wantLen)
		}
		for i, e := range entries {
			if e.Seq != uint64(i+1) || e.TS.IsZero() || len(e.Payload) == 0 {
				t.Fatalf("%s entry %d malformed: %+v", name, i, e)
			}
		}
	}
}

func TestDecodeLogErrors(t *testing.T) {
	const ok = `{"seq":1,"ts":"2026-01-01T00:00:00Z","dir":"out","payload":{}}`
	tests := []struct {
		name    string
		log     string
		wantErr string
	}{
		{"bad json", ok + "\n{nope\n", "line 2"},
		{"unknown dir", `{"seq":1,"ts":"2026-01-01T00:00:00Z","dir":"sideways","payload":{}}`, `unknown dir "sideways"`},
		{"repeated seq", ok + "\n" + ok, "seq 1 does not follow 1"},
		{"decreasing seq", `{"seq":5,"ts":"2026-01-01T00:00:00Z","dir":"out","payload":{}}` + "\n" + `{"seq":4,"ts":"2026-01-01T00:00:00Z","dir":"in","payload":{}}`, "seq 4 does not follow 5"},
		{"zero seq", `{"seq":0,"ts":"2026-01-01T00:00:00Z","dir":"out","payload":{}}`, "seq 0"},
		{"bad timestamp", `{"seq":1,"ts":"yesterday","dir":"out","payload":{}}`, "line 1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := DecodeLog(strings.NewReader(tt.log))
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("err = %v, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

func TestDecodeLogTolerates(t *testing.T) {
	line := `{"seq":1,"ts":"2026-01-01T00:00:00Z","dir":"out","payload":{"type":"x"}}`
	long := `{"seq":2,"ts":"2026-01-01T00:00:00Z","dir":"out","payload":{"type":"x","pad":"` + strings.Repeat("a", 1<<20) + `"}}`
	tests := []struct {
		name string
		log  string
		want int
	}{
		{"empty", "", 0},
		{"no trailing newline", line, 1},
		{"blank lines and CRLF", "\n" + line + "\r\n\n", 1},
		{"line over one MiB", line + "\n" + long + "\n", 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entries, err := DecodeLog(strings.NewReader(tt.log))
			if err != nil || len(entries) != tt.want {
				t.Fatalf("got %d entries, err %v; want %d", len(entries), err, tt.want)
			}
		})
	}
}

func TestParseHostNote(t *testing.T) {
	zero := 0
	tests := []struct {
		name    string
		payload string
		want    HostNote
		wantErr bool
	}{
		{"spawn", `{"kind":"spawn","resume":"sess-1","argv":["claude"]}`, HostNote{Kind: NoteSpawn, Resume: "sess-1", Argv: []string{"claude"}}, false},
		{"exit", `{"kind":"exit","code":0,"signal":null}`, HostNote{Kind: NoteExit, Code: &zero}, false},
		{"exit on signal", `{"kind":"exit","code":null,"signal":"SIGKILL"}`, HostNote{Kind: NoteExit}, false},
		{"user origin", `{"kind":"user_origin","uuid":"u1","origin":{"via":"ao_send","from":"w-1"}}`, HostNote{Kind: NoteUserOrigin, UUID: "u1", Origin: &Origin{Via: "ao_send", From: "w-1"}}, false},
		{"unknown kind is kept", `{"kind":"future"}`, HostNote{Kind: "future"}, false},
		{"missing kind", `{}`, HostNote{}, true},
		{"not json", `nope`, HostNote{}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseHostNote(json.RawMessage(tt.payload))
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if string(got.Raw) != tt.payload {
				t.Errorf("Raw = %s, want the payload", got.Raw)
			}
			got.Raw = nil
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestHostNotesInFixtures(t *testing.T) {
	var kinds []HostNoteKind
	for _, e := range loadFixture(t, "first_session.jsonl") {
		if e.Dir != DirHost {
			continue
		}
		n, err := ParseHostNote(e.Payload)
		if err != nil {
			t.Fatal(err)
		}
		kinds = append(kinds, n.Kind)
	}
	if want := []HostNoteKind{NoteSpawn, NoteUserOrigin, NoteExit}; !reflect.DeepEqual(kinds, want) {
		t.Errorf("host note kinds = %v, want %v", kinds, want)
	}
}
