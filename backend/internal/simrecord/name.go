// Package simrecord is where a recording becomes a file a human can find.
//
// It owns three things the rest of the system should not have to know about:
// what a recorded flow is called, where it lives on disk, and how a stored
// recording becomes the flow's own text. They are together because they are
// one decision seen from three sides - a rename has to keep the timestamp the
// listing sorts by, and the listing has to read the counts the emitter wrote.
package simrecord

import (
	"regexp"
	"strings"
	"time"
	"unicode"
)

// stampLayout keeps millisecond precision, matching the layout `ao sim shot`
// uses for screenshots. It is what makes two recordings a second apart - or
// two recordings a human gave the SAME name - land on different files without
// anyone being asked to resolve an overwrite.
const stampLayout = "20060102-150405.000"

// flowExt is the only extension this package writes or lists.
const flowExt = ".yaml"

// maxSlugRunes bounds a name so a human pasting a sentence cannot produce a
// path the filesystem refuses. Runes, not bytes: a Thai name is three bytes
// per character, and cutting at a byte count would both truncate far shorter
// than a reader expects and risk splitting a character in half.
const maxSlugRunes = 60

// stampPattern finds the recording's own timestamp inside a file name.
//
// It is searched for rather than anchored because this package must read
// three shapes of name and only writes one: `<slug>-<stamp>Z.yaml` and
// `<stamp>Z.yaml`, which it writes, and `<stamp>Z-<udid>.yaml`, which is what
// every flow recorded before names existed is called. Those older files stay
// exactly where they are and keep working; a listing that could not read them
// would look like they had been lost.
var stampPattern = regexp.MustCompile(`(\d{8}-\d{6}\.\d{3})Z`)

// Slug turns what a human typed into the name part of a file.
//
// It keeps letters, digits AND combining marks of any script - a Thai name
// survives as Thai, because the person who typed it is the person who has to
// recognise it in a list - and turns everything else into a single separator.
//
// ⚠ The marks are the part that is easy to get wrong, and getting it wrong is
// silent. Thai vowel signs and tone marks are Unicode marks, not letters, so a
// rule built on unicode.IsLetter alone shreds a Thai word into fragments
// (เข้าสู่ระบบ becomes "เข-าส-ระบบ") while every ASCII test still passes. The
// same applies to Devanagari, Arabic and decomposed Vietnamese. That rule is also
// what makes the result safe by construction: no separator, no dot and no
// colon can survive it, so a slug can never climb out of the directory it is
// joined to, and there is no second sanitising step that could be forgotten.
//
// An empty result is normal, not an error: it means the recording is unnamed,
// and FileName falls back to the timestamp alone. Naming is something a human
// does when they decide a recording is worth keeping - refusing to write a
// file because they have not decided yet would lose the gestures.
func Slug(name string) string {
	var b strings.Builder
	pendingSep := false
	written := 0
	for _, r := range name {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsMark(r) {
			if pendingSep && written > 0 {
				b.WriteRune('-')
				written++
			}
			pendingSep = false
			if written >= maxSlugRunes {
				break
			}
			b.WriteRune(unicode.ToLower(r))
			written++
			continue
		}
		pendingSep = true
	}
	return strings.Trim(b.String(), "-")
}

// FileName is the name a recording is written under: the human's own words,
// then the moment it was recorded.
//
// The timestamp is a suffix rather than a prefix so a list sorted by name
// groups the attempts at one flow together, which is how the recordings
// actually accumulate - a human records the same path several times before it
// is right. It is never omitted, so naming two recordings the same thing is
// simply two files rather than a question about overwriting one.
func FileName(name string, recordedAt time.Time) string {
	stamp := recordedAt.UTC().Format(stampLayout) + "Z"
	if slug := Slug(name); slug != "" {
		return slug + "-" + stamp + flowExt
	}
	return stamp + flowExt
}

// ParseFileName recovers what a file name says: the name a human gave it, and
// when it was recorded.
//
// ok is false when there is no timestamp to read - the file was written by
// something else, or renamed by hand. The caller then falls back to the file's
// own modification time, which is a worse answer about when it was recorded
// but an honest one about the file.
func ParseFileName(base string) (name string, recordedAt time.Time, ok bool) {
	trimmed := strings.TrimSuffix(base, flowExt)
	loc := stampPattern.FindStringSubmatchIndex(trimmed)
	if loc == nil {
		return "", time.Time{}, false
	}
	at, err := time.ParseInLocation(stampLayout, trimmed[loc[2]:loc[3]], time.UTC)
	if err != nil {
		return "", time.Time{}, false
	}
	// Everything before the stamp is the name. Everything after it is the udid
	// the pre-naming file names carried, which is not a name and must not be
	// shown as one.
	return strings.Trim(trimmed[:loc[0]], "-"), at, true
}
