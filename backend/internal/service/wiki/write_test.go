package wiki

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

func hashOf(t *testing.T, s string) string {
	t.Helper()
	return ContentHash([]byte(s))
}

func codeOf(t *testing.T, err error) string {
	t.Helper()
	var apiErr *apierr.Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("not an API error: %v", err)
	}
	return apiErr.Code
}

func TestWriteNote_ReplacesTheBytesItWasGiven(t *testing.T) {
	vault := t.TempDir()
	const before = "---\ntitle: Tasks\n---\n\n- [ ] one\n- [ ] two\n"
	const after = "---\ntitle: Tasks\n---\n\n- [x] one\n- [ ] two\n"
	mustWrite(t, filepath.Join(vault, "notes", "tasks.md"), before)
	svc, _, _, _ := newService(vault)

	res, err := svc.WriteNote(context.Background(), WriteNoteInput{
		Path:     "notes/tasks.md",
		Content:  after,
		BaseHash: hashOf(t, before),
	})
	if err != nil {
		t.Fatal(err)
	}
	on, err := os.ReadFile(filepath.Join(vault, "notes", "tasks.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(on) != after {
		t.Fatalf("on disk = %q", on)
	}
	if res.ContentHash != hashOf(t, after) || res.Path != "notes/tasks.md" || res.Size != int64(len(after)) {
		t.Fatalf("result = %+v", res)
	}
}

// The whole point of the hash: the vault's own agent writes these files, so a
// save against bytes that have moved must be refused rather than merged.
func TestWriteNote_RefusesAStaleBaseHash(t *testing.T) {
	vault := t.TempDir()
	path := filepath.Join(vault, "note.md")
	mustWrite(t, path, "original\n")
	svc, _, _, _ := newService(vault)
	stale := hashOf(t, "original\n")

	mustWrite(t, path, "the agent wrote this\n")

	_, err := svc.WriteNote(context.Background(), WriteNoteInput{Path: "note.md", Content: "mine\n", BaseHash: stale})
	if err == nil {
		t.Fatal("a stale write must be refused")
	}
	if code := codeOf(t, err); code != "WIKI_NOTE_CONFLICT" {
		t.Fatalf("code = %s", code)
	}
	on, _ := os.ReadFile(path)
	if string(on) != "the agent wrote this\n" {
		t.Fatalf("the refused write still landed: %q", on)
	}
}

func TestWriteNote_ConflictNamesWhatIsOnDiskNow(t *testing.T) {
	vault := t.TempDir()
	mustWrite(t, filepath.Join(vault, "note.md"), "now\n")
	svc, _, _, _ := newService(vault)

	_, err := svc.WriteNote(context.Background(), WriteNoteInput{Path: "note.md", Content: "x", BaseHash: hashOf(t, "then\n")})
	var apiErr *apierr.Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("not an API error: %v", err)
	}
	if apiErr.Details["currentHash"] != hashOf(t, "now\n") {
		t.Fatalf("details = %+v", apiErr.Details)
	}
	if apiErr.Details["currentSize"] != len("now\n") {
		t.Fatalf("details = %+v", apiErr.Details)
	}
}

func TestWriteNote_RequiresABaseHash(t *testing.T) {
	vault := t.TempDir()
	mustWrite(t, filepath.Join(vault, "note.md"), "body\n")
	svc, _, _, _ := newService(vault)

	_, err := svc.WriteNote(context.Background(), WriteNoteInput{Path: "note.md", Content: "x"})
	if err == nil {
		t.Fatal("a write with no precondition must be refused")
	}
	if code := codeOf(t, err); code != "WIKI_NOTE_BASE_HASH_REQUIRED" {
		t.Fatalf("code = %s", code)
	}
	on, _ := os.ReadFile(filepath.Join(vault, "note.md"))
	if string(on) != "body\n" {
		t.Fatalf("the refused write still landed: %q", on)
	}
}

// Reading anywhere on disk through a no-auth loopback daemon is a viewer
// convenience; writing anywhere on disk is an escalation.
func TestWriteNote_RefusesEscapingTheVault(t *testing.T) {
	parent := t.TempDir()
	vault := filepath.Join(parent, "vault")
	mustWrite(t, filepath.Join(vault, "ok.md"), "ok\n")
	mustWrite(t, filepath.Join(parent, "secret.md"), "secret\n")
	svc, _, _, _ := newService(vault)

	for _, bad := range []string{"../secret.md", "/etc/hosts", "~/.ssh/id_rsa", "", "sub/../../secret.md"} {
		_, err := svc.WriteNote(context.Background(), WriteNoteInput{
			Path: bad, Content: "owned\n", BaseHash: hashOf(t, "secret\n"),
		})
		if err == nil {
			t.Fatalf("WriteNote(%q) must be refused", bad)
		}
	}
	on, _ := os.ReadFile(filepath.Join(parent, "secret.md"))
	if string(on) != "secret\n" {
		t.Fatalf("a write escaped the vault: %q", on)
	}
}

func TestWriteNote_RefusesASymlinkOutOfTheVault(t *testing.T) {
	parent := t.TempDir()
	vault := filepath.Join(parent, "vault")
	mustWrite(t, filepath.Join(vault, "ok.md"), "ok\n")
	mustWrite(t, filepath.Join(parent, "secret.md"), "secret\n")
	if err := os.Symlink(filepath.Join(parent, "secret.md"), filepath.Join(vault, "link.md")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	svc, _, _, _ := newService(vault)

	_, err := svc.WriteNote(context.Background(), WriteNoteInput{
		Path: "link.md", Content: "owned\n", BaseHash: hashOf(t, "secret\n"),
	})
	if err == nil {
		t.Fatal("a symlink out of the vault must be refused")
	}
	on, _ := os.ReadFile(filepath.Join(parent, "secret.md"))
	if string(on) != "secret\n" {
		t.Fatalf("a write escaped the vault through a symlink: %q", on)
	}
}

// Save-what-you-opened is the whole use case, so the route never creates a file
// it did not find.
func TestWriteNote_RefusesANoteThatIsNotThere(t *testing.T) {
	vault := t.TempDir()
	svc, _, _, _ := newService(vault)

	_, err := svc.WriteNote(context.Background(), WriteNoteInput{
		Path: "new.md", Content: "hello\n", BaseHash: hashOf(t, ""),
	})
	if code := codeOf(t, err); code != "WIKI_NOTE_NOT_FOUND" {
		t.Fatalf("code = %s", code)
	}
	if _, statErr := os.Stat(filepath.Join(vault, "new.md")); !os.IsNotExist(statErr) {
		t.Fatal("the write created a note that was not there")
	}
}

func TestWriteNote_KeepsThePermissionBits(t *testing.T) {
	vault := t.TempDir()
	path := filepath.Join(vault, "note.md")
	mustWrite(t, path, "body\n")
	if err := os.Chmod(path, 0o640); err != nil {
		t.Fatal(err)
	}
	svc, _, _, _ := newService(vault)

	if _, err := svc.WriteNote(context.Background(), WriteNoteInput{
		Path: "note.md", Content: "new\n", BaseHash: hashOf(t, "body\n"),
	}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Fatalf("mode = %v", info.Mode().Perm())
	}
}

// The read hands out the token the write demands; a round trip through both
// must not need a second read to keep saving.
func TestReadNote_HandsOutThePreconditionTheWriteWants(t *testing.T) {
	vault := t.TempDir()
	mustWrite(t, filepath.Join(vault, "note.md"), "one\n")
	svc, _, _, _ := newService(vault)

	note, err := svc.ReadNote(context.Background(), "note.md")
	if err != nil {
		t.Fatal(err)
	}
	first, err := svc.WriteNote(context.Background(), WriteNoteInput{
		Path: "note.md", Content: "two\n", BaseHash: note.ContentHash,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.WriteNote(context.Background(), WriteNoteInput{
		Path: "note.md", Content: "three\n", BaseHash: first.ContentHash,
	}); err != nil {
		t.Fatalf("the hash the write returned must precondition the next one: %v", err)
	}
}
