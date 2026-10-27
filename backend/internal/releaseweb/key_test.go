package releaseweb

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
)

func secretOf(t *testing.T, p Paths) string {
	t.Helper()
	b, err := os.ReadFile(p.KeyFile())
	if err != nil {
		t.Fatalf("read key file: %v", err)
	}
	var f keyFile
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatal("key file is not JSON")
	}
	return f.Key
}

func writeKeyFile(t *testing.T, p Paths, key string) {
	t.Helper()
	if err := p.Ensure(); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(keyFile{Key: key, Generation: 1})
	if err := os.WriteFile(p.KeyFile(), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestRotateKeyWritesAFreshPrivateKeyWithTheNextGeneration(t *testing.T) {
	p := Paths{Dir: t.TempDir() + "/web"}
	if err := RotateKey(p); err != nil {
		t.Fatal(err)
	}
	first := secretOf(t, p)
	raw, err := base64.RawURLEncoding.DecodeString(first)
	if err != nil || len(raw) < 32 {
		t.Fatalf("rotated key decodes to %d bytes (err %v), want >= 32", len(raw), err)
	}
	for path, want := range map[string]os.FileMode{p.KeyFile(): 0o600, p.Dir: 0o700} {
		st, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := st.Mode().Perm(); got != want {
			t.Errorf("%s mode %o, want %o", path, got, want)
		}
	}
	k1, err := ReadKey(p)
	if err != nil || k1.Generation != 1 {
		t.Fatalf("after first rotation: generation %d, err %v", k1.Generation, err)
	}

	if err := RotateKey(p); err != nil {
		t.Fatal(err)
	}
	k2, err := ReadKey(p)
	if err != nil || k2.Generation != 2 {
		t.Fatalf("after second rotation: generation %d, err %v", k2.Generation, err)
	}
	if secretOf(t, p) == first {
		t.Error("second rotation kept the same key")
	}
	if k2.Matches(first) || !k2.Matches(secretOf(t, p)) {
		t.Error("rotated key matches the old secret or not the new one")
	}
}

func TestReadKeyRefusesWeakOrMalformedKeys(t *testing.T) {
	cases := map[string]string{
		"short":        base64.RawURLEncoding.EncodeToString(make([]byte, 15)),
		"padded":       base64.URLEncoding.EncodeToString(make([]byte, 31)),
		"std alphabet": strings.Repeat("+/", 16),
		"not base64":   "correct horse battery staple!!",
		"empty":        "",
	}
	for name, key := range cases {
		t.Run(name, func(t *testing.T) {
			p := Paths{Dir: t.TempDir()}
			writeKeyFile(t, p, key)
			_, err := ReadKey(p)
			if err == nil {
				t.Fatal("ReadKey accepted the key file")
			}
			if key != "" && strings.Contains(err.Error(), key) {
				t.Error("error echoes the key value")
			}
		})
	}

	p := Paths{Dir: t.TempDir()}
	if _, err := ReadKey(p); !errors.Is(err, ErrNoKey) {
		t.Errorf("missing key file gave %v, want ErrNoKey", err)
	}
	writeKeyFile(t, p, base64.RawURLEncoding.EncodeToString(make([]byte, 16)))
	if _, err := ReadKey(p); err != nil {
		t.Errorf("16-byte key refused: %v", err)
	}
}

func TestKeyNeverPrintsItsSecret(t *testing.T) {
	p := Paths{Dir: t.TempDir()}
	if err := RotateKey(p); err != nil {
		t.Fatal(err)
	}
	k, err := ReadKey(p)
	if err != nil {
		t.Fatal(err)
	}
	secret := secretOf(t, p)
	js, err := json.Marshal(k)
	if err != nil {
		t.Fatal(err)
	}
	for format, out := range map[string]string{
		"%v":     fmt.Sprintf("%v", k),
		"%+v":    fmt.Sprintf("%+v", k),
		"%#v":    fmt.Sprintf("%#v", k),
		"String": k.String(),
		"json":   string(js),
	} {
		if strings.Contains(out, secret) {
			t.Errorf("%s output contains the secret", format)
		}
		if !strings.Contains(out, "1") {
			t.Errorf("%s output %q lost the generation", format, out)
		}
	}
}

func TestCopyKeyWritesTheRawKeyAndRefusesAMissingOne(t *testing.T) {
	p := Paths{Dir: t.TempDir() + "/web"}
	var none strings.Builder
	if err := CopyKey(p, &none); !errors.Is(err, ErrNoKey) || none.Len() != 0 {
		t.Fatalf("CopyKey without a key = %v, wrote %d bytes", err, none.Len())
	}
	if err := RotateKey(p); err != nil {
		t.Fatal(err)
	}
	var got strings.Builder
	if err := CopyKey(p, &got); err != nil {
		t.Fatal(err)
	}
	if got.String() != secretOf(t, p) {
		t.Fatal("CopyKey did not write the key file's key")
	}
}
