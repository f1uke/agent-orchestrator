package chatblob

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var png = []byte("\x89PNG\r\n\x1a\nnot really a png <&>")

func readResult(b64 string) string {
	return `{"type":"user","message":{"role":"user","content":[{"tool_use_id":"t1","type":"tool_result","content":[{"type":"image","source":{"type":"base64","data":"` + b64 + `","media_type":"image/png"}}]}]},"tool_use_result":{"type":"image","file":{"base64":"` + b64 + `","type":"image/png","originalSize":42}},"note":"a < b && c > d","n":1.50}`
}

func blobName(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]) + ".png"
}

func TestExternalizeStoresAReadImageOnceAndDropsTheDuplicate(t *testing.T) {
	s := New(t.TempDir())
	b64 := base64.StdEncoding.EncodeToString(png)
	got, err := s.Externalize(json.RawMessage(readResult(b64)))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got), b64) {
		t.Fatalf("payload still carries the image bytes: %s", got)
	}
	name := blobName(png)
	want := `{"type":"user","message":{"role":"user","content":[{"tool_use_id":"t1","type":"tool_result","content":[{"type":"image","source":{"type":"ao_blob","blob":"` + name + `","media_type":"image/png","size":` + itoa(len(png)) + `}}]}]},"tool_use_result":{"type":"image","file":{"blob":"` + name + `","type":"image/png","originalSize":42}},"note":"a < b && c > d","n":1.50}`
	assertSameJSON(t, got, want)
	if !strings.Contains(string(got), `"a < b && c > d"`) || !strings.Contains(string(got), `1.50`) {
		t.Errorf("re-encoding changed text or numbers: %s", got)
	}
	stored, err := os.ReadFile(filepath.Join(s.Dir(), name))
	if err != nil || !bytes.Equal(stored, png) {
		t.Fatalf("blob %s = %q, %v; want the decoded image", name, stored, err)
	}
	entries, _ := os.ReadDir(s.Dir())
	if len(entries) != 1 {
		t.Errorf("blob dir holds %d files, want 1", len(entries))
	}
}

func TestExternalizeIsIdempotent(t *testing.T) {
	s := New(t.TempDir())
	once, err := s.Externalize(json.RawMessage(readResult(base64.StdEncoding.EncodeToString(png))))
	if err != nil {
		t.Fatal(err)
	}
	twice, err := s.Externalize(once)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(once, twice) {
		t.Errorf("second pass changed the payload:\n%s\n%s", once, twice)
	}
}

func TestExternalizeLeavesPayloadsWithoutImagesByteForByte(t *testing.T) {
	s := New(filepath.Join(t.TempDir(), "blobs"))
	for _, p := range []string{
		`{"type":"assistant","message":{"content":[{"type":"text","text":"the word base64 <b>"}]}}`,
		`{"type":"user","message":{"content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"!!not base64!!"}}]}}`,
		`{"type":"user","message":{"content":[{"type":"image","source":{"type":"url","url":"https://x/y.png"}}]}}`,
	} {
		got, err := s.Externalize(json.RawMessage(p))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != p {
			t.Errorf("changed:\n got %s\nwant %s", got, p)
		}
	}
	if _, err := os.Stat(s.Dir()); !os.IsNotExist(err) {
		t.Errorf("blob dir created for payloads with nothing to store: %v", err)
	}
}

func TestInlineRestoresOneCopyOfTheImage(t *testing.T) {
	s := New(t.TempDir())
	b64 := base64.StdEncoding.EncodeToString(png)
	ref, err := s.Externalize(json.RawMessage(readResult(b64)))
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Inline(ref)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(got), b64); n != 1 {
		t.Fatalf("inlined payload carries the image %d times, want 1: %s", n, got)
	}
	var w struct {
		Message struct {
			Content []struct {
				Content []struct {
					Type   string
					Source struct {
						Type      string `json:"type"`
						MediaType string `json:"media_type"`
						Data      string `json:"data"`
					}
				}
			}
		}
	}
	if err := json.Unmarshal(got, &w); err != nil {
		t.Fatal(err)
	}
	src := w.Message.Content[0].Content[0].Source
	if src.Type != "base64" || src.MediaType != "image/png" || src.Data != b64 {
		t.Errorf("source = %+v, want the original base64 source", src)
	}
}

func TestInlineLeavesARefWhoseBlobIsMissing(t *testing.T) {
	s := New(t.TempDir())
	p := `{"type":"user","message":{"content":[{"type":"image","source":{"type":"ao_blob","media_type":"image/png","blob":"` + blobName(png) + `","size":3}}]}}`
	got, err := s.Inline(json.RawMessage(p))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != p {
		t.Errorf("changed:\n got %s\nwant %s", got, p)
	}
}

func TestPathAcceptsOnlyBlobNames(t *testing.T) {
	s := New("/data/sessions/s1/blobs")
	name := blobName(png)
	if p, ok := s.Path(name); !ok || p != filepath.Join("/data/sessions/s1/blobs", name) {
		t.Errorf("Path(%q) = %q, %v", name, p, ok)
	}
	for _, bad := range []string{"", "../events.jsonl", name[:10] + ".png", strings.ToUpper(name[:64]) + ".png", name[:64] + ".exe", name[:64], name + "/x"} {
		if _, ok := s.Path(bad); ok {
			t.Errorf("Path(%q) accepted", bad)
		}
	}
}

func TestForLogPutsBlobsNextToTheLog(t *testing.T) {
	if got, want := ForLog("/data/sessions/s1/events.jsonl").Dir(), "/data/sessions/s1/blobs"; got != want {
		t.Errorf("dir = %q, want %q", got, want)
	}
}

func assertSameJSON(t *testing.T, got []byte, want string) {
	t.Helper()
	var g, w any
	if err := json.Unmarshal(got, &g); err != nil {
		t.Fatalf("got is not JSON: %v\n%s", err, got)
	}
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatalf("want is not JSON: %v", err)
	}
	gb, _ := json.Marshal(g)
	wb, _ := json.Marshal(w)
	if !bytes.Equal(gb, wb) {
		t.Errorf("JSON differs\n got %s\nwant %s", gb, wb)
	}
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }
