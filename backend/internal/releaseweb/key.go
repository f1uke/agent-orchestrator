package releaseweb

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/fsatomic"
)

const (
	keyBytes    = 32
	minKeyBytes = 16
)

// ErrNoKey means key.json does not exist yet.
var ErrNoKey = errors.New("release web: no key yet; rotate the key first")

// ErrNoClipboard means this machine has no clipboard command.
var ErrNoClipboard = errors.New("release web: no clipboard command on this OS; read the key file instead")

var keyEncoding = base64.RawURLEncoding.Strict()

type keyFile struct {
	Key        string    `json:"key"`
	Generation int       `json:"generation"`
	CreatedAt  time.Time `json:"createdAt"`
}

// Key is the loaded shared key; every way of printing it redacts.
type Key struct {
	digest     [sha256.Size]byte
	id         keyID
	Generation int
}

type keyID [16]byte

func idOf(secret string) keyID {
	sum := sha256.Sum256([]byte("release web key id\x00" + secret))
	return keyID(sum[:16])
}

// Matches compares a submitted key in constant time.
func (k Key) Matches(submitted string) bool {
	d := sha256.Sum256([]byte(submitted))
	return subtle.ConstantTimeCompare(d[:], k.digest[:]) == 1
}

// String redacts the secret.
func (k Key) String() string {
	return fmt.Sprintf("releaseweb.Key{Generation: %d, secret: redacted}", k.Generation)
}

// GoString redacts the secret under %#v.
func (k Key) GoString() string { return k.String() }

// MarshalJSON writes only the generation.
func (k Key) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Generation int `json:"generation"`
	}{k.Generation})
}

// ReadKey loads and validates key.json.
func ReadKey(p Paths) (Key, error) {
	f, err := readKeyFile(p)
	if err != nil {
		return Key{}, err
	}
	return Key{digest: sha256.Sum256([]byte(f.Key)), id: idOf(f.Key), Generation: f.Generation}, nil
}

// CopyKey writes the raw key to w with no newline.
func CopyKey(p Paths, w io.Writer) error {
	f, err := readKeyFile(p)
	if err != nil {
		return err
	}
	if _, err := io.WriteString(w, f.Key); err != nil {
		return fmt.Errorf("release web: write key: %w", err)
	}
	return nil
}

func readKeyFile(p Paths) (keyFile, error) {
	b, err := os.ReadFile(p.KeyFile())
	if errors.Is(err, os.ErrNotExist) {
		return keyFile{}, ErrNoKey
	}
	if err != nil {
		return keyFile{}, fmt.Errorf("release web: read key file: %w", err)
	}
	var f keyFile
	if err := json.Unmarshal(b, &f); err != nil {
		return keyFile{}, fmt.Errorf("release web: %s is not valid JSON", p.KeyFile())
	}
	raw, err := keyEncoding.DecodeString(f.Key)
	if err != nil {
		return keyFile{}, fmt.Errorf("release web: the key in %s is not base64url without padding", p.KeyFile())
	}
	if len(raw) < minKeyBytes {
		return keyFile{}, fmt.Errorf("release web: the key in %s decodes to %d bytes, need at least %d", p.KeyFile(), len(raw), minKeyBytes)
	}
	return f, nil
}

// RotateKey writes a fresh random key with the next generation.
func RotateKey(p Paths) error {
	if err := p.Ensure(); err != nil {
		return err
	}
	gen := 0
	if b, err := os.ReadFile(p.KeyFile()); err == nil {
		var prev keyFile
		if json.Unmarshal(b, &prev) == nil {
			gen = prev.Generation
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("release web: read key file: %w", err)
	}
	raw := make([]byte, keyBytes)
	if _, err := rand.Read(raw); err != nil {
		return fmt.Errorf("release web: generate key: %w", err)
	}
	data, err := json.MarshalIndent(keyFile{
		Key:        keyEncoding.EncodeToString(raw),
		Generation: gen + 1,
		CreatedAt:  time.Now().UTC(),
	}, "", "  ")
	if err != nil {
		return fmt.Errorf("release web: encode key file: %w", err)
	}
	if err := fsatomic.WriteFile(p.KeyFile(), append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("release web: write key file: %w", err)
	}
	return nil
}
