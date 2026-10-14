package releasetrain

import "testing"

func TestParseTrainLabel(t *testing.T) {
	for _, tc := range []struct {
		in       string
		ok       bool
		prefix   string
		id       TrainID
		key, str string
	}{
		{"Release Train #21/2026", true, "", TrainID{21, 0, 2026}, "21-2026", "#21"},
		{"release train #21/2026", true, "", TrainID{21, 0, 2026}, "21-2026", "#21"},
		{"Pilot Release Train #20-1/2026", true, "Pilot", TrainID{20, 1, 2026}, "20-1-2026", "#20-1"},
		{"Scope for pilot RELEASE TRAIN #7/2027 and more", true, "pilot", TrainID{7, 0, 2027}, "7-2027", "#7"},
		{"Hotfix crash on launch", false, "", TrainID{}, "", ""},
		{"Release Train #21", false, "", TrainID{}, "", ""},
		{"Release Train 21/2026", false, "", TrainID{}, "", ""},
	} {
		t.Run(tc.in, func(t *testing.T) {
			prefix, id, ok := ParseTrainLabel(tc.in)
			if ok != tc.ok || prefix != tc.prefix || id != tc.id {
				t.Fatalf("ParseTrainLabel = (%q, %+v, %v), want (%q, %+v, %v)", prefix, id, ok, tc.prefix, tc.id, tc.ok)
			}
			if ok && (id.Key() != tc.key || id.String() != tc.str) {
				t.Fatalf("Key/String = %s/%s, want %s/%s", id.Key(), id.String(), tc.key, tc.str)
			}
		})
	}
}

func TestParseVersion(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want Version
		ok   bool
	}{
		{"Release 3.4.0", Version{3, 4, 0}, true},
		{"iOS Pilot 10.0.12", Version{10, 0, 12}, true},
		{"3.4", Version{}, false},
		{"none", Version{}, false},
	} {
		got, ok := ParseVersion(tc.in)
		if got != tc.want || ok != tc.ok {
			t.Fatalf("ParseVersion(%q) = %v, %v; want %v, %v", tc.in, got, ok, tc.want, tc.ok)
		}
		if ok && got.String() != tc.in[len(tc.in)-len(got.String()):] {
			t.Fatalf("String() = %q", got.String())
		}
	}
}

func TestVersionCompare(t *testing.T) {
	for _, tc := range []struct {
		a, b Version
		want int
	}{
		{Version{3, 4, 0}, Version{3, 4, 0}, 0},
		{Version{3, 10, 0}, Version{3, 9, 9}, 1},
		{Version{2, 99, 99}, Version{3, 0, 0}, -1},
		{Version{3, 4, 1}, Version{3, 4, 0}, 1},
	} {
		if got := tc.a.Compare(tc.b); got != tc.want {
			t.Fatalf("%v.Compare(%v) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestParseExactVersionRejectsSuffixes(t *testing.T) {
	for in, ok := range map[string]bool{"3.4.0": true, "3.4.0-rc1": false, "v3.4.0": false, "3.4": false} {
		if _, got := parseExactVersion(in); got != ok {
			t.Fatalf("parseExactVersion(%q) ok = %v, want %v", in, got, ok)
		}
	}
}
