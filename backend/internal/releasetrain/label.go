package releasetrain

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
)

// TrainID identifies a train: "#21" is {21, 0, year}, "#20-1" is {20, 1, year}.
type TrainID struct {
	Number int
	Extra  int
	Year   int
}

// Key is the train's stable key, "21-2026" or "20-1-2026".
func (t TrainID) Key() string {
	if t.Extra > 0 {
		return fmt.Sprintf("%d-%d-%d", t.Number, t.Extra, t.Year)
	}
	return fmt.Sprintf("%d-%d", t.Number, t.Year)
}

// String is the train's display name, "#21" or "#20-1".
func (t TrainID) String() string {
	if t.Extra > 0 {
		return fmt.Sprintf("#%d-%d", t.Number, t.Extra)
	}
	return fmt.Sprintf("#%d", t.Number)
}

// MarshalJSON writes the train's key, "21-2026".
func (t TrainID) MarshalJSON() ([]byte, error) { return json.Marshal(t.Key()) }

// UnmarshalJSON reads a train key.
func (t *TrainID) UnmarshalJSON(b []byte) error {
	var key string
	if err := json.Unmarshal(b, &key); err != nil {
		return err
	}
	id, ok := ParseTrainKey(key)
	if !ok {
		return fmt.Errorf("train key %q is not like 21-2026", key)
	}
	*t = id
	return nil
}

func (t TrainID) less(o TrainID) bool {
	if t.Year != o.Year {
		return t.Year < o.Year
	}
	if t.Number != o.Number {
		return t.Number < o.Number
	}
	return t.Extra < o.Extra
}

var trainLabelRE = regexp.MustCompile(`(?i)(?:(\S+)\s+)?release\s+train\s+#(\d+)(?:-(\d+))?/(\d{4})`)

// ParseTrainLabel finds "<Prefix> Release Train #NN/YYYY" in s; prefix is the word before "Release Train", if any.
func ParseTrainLabel(s string) (prefix string, id TrainID, ok bool) {
	m := trainLabelRE.FindStringSubmatch(s)
	if m == nil {
		return "", TrainID{}, false
	}
	id.Number, _ = strconv.Atoi(m[2])
	if m[3] != "" {
		id.Extra, _ = strconv.Atoi(m[3])
	}
	id.Year, _ = strconv.Atoi(m[4])
	return m[1], id, true
}

// Version is a semantic app version X.Y.Z.
type Version struct{ Major, Minor, Patch int }

var (
	versionRE      = regexp.MustCompile(`(\d+)\.(\d+)\.(\d+)`)
	exactVersionRE = regexp.MustCompile(`^(\d+)\.(\d+)\.(\d+)$`)
)

// ParseVersion finds the first X.Y.Z in s.
func ParseVersion(s string) (Version, bool) { return versionFrom(versionRE.FindStringSubmatch(s)) }

func parseExactVersion(s string) (Version, bool) {
	return versionFrom(exactVersionRE.FindStringSubmatch(s))
}

func versionFrom(m []string) (Version, bool) {
	if m == nil {
		return Version{}, false
	}
	var v Version
	v.Major, _ = strconv.Atoi(m[1])
	v.Minor, _ = strconv.Atoi(m[2])
	v.Patch, _ = strconv.Atoi(m[3])
	return v, true
}

// String formats v as "X.Y.Z".
func (v Version) String() string { return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch) }

// Compare returns -1, 0 or 1 as v is older than, equal to or newer than o.
func (v Version) Compare(o Version) int {
	for _, d := range [3]int{v.Major - o.Major, v.Minor - o.Minor, v.Patch - o.Patch} {
		if d != 0 {
			if d > 0 {
				return 1
			}
			return -1
		}
	}
	return 0
}
