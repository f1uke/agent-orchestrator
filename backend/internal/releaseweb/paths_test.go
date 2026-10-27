package releaseweb

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestDirForSitsBesideTheDataDirUnlessTheEnvNamesOne(t *testing.T) {
	t.Setenv("AO_RELEASE_WEB_DIR", "")
	data := filepath.Join("home", ".ao", "data")
	if got, want := DirFor(data), filepath.Join("home", ".ao", "release-web"); got != want {
		t.Fatalf("DirFor(%q) = %q, want %q", data, got, want)
	}
	sandbox := filepath.Join("tmp", "sandbox", "data")
	if got, want := DirFor(sandbox), filepath.Join("tmp", "sandbox", "release-web"); got != want {
		t.Fatalf("a sandbox data dir must keep its own release web: got %q, want %q", got, want)
	}
	t.Setenv("AO_RELEASE_WEB_DIR", "/custom/web")
	if got := DirFor(data); got != "/custom/web" {
		t.Fatalf("DirFor with the env set = %q", got)
	}
}

func TestHostsFileKeepsCanonicalNamesAndRefusesAnythingElse(t *testing.T) {
	p := Paths{Dir: t.TempDir()}
	if hosts, err := ReadHosts(p); err != nil || hosts != nil {
		t.Fatalf("ReadHosts without a file = %v, %v", hosts, err)
	}
	if err := WriteHosts(p, []string{"Box.Tail1234.TS.NET.", "box.tail1234.ts.net", "board.example.org"}); err != nil {
		t.Fatal(err)
	}
	hosts, err := ReadHosts(p)
	if err != nil || !slices.Equal(hosts, []string{"box.tail1234.ts.net", "board.example.org"}) {
		t.Fatalf("ReadHosts = %v, %v", hosts, err)
	}
	if err := os.WriteFile(p.HostsFile(), []byte("box.tail1234.ts.net\nhttps://board.example.org/\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadHosts(p); err == nil || !strings.Contains(err.Error(), p.HostsFile()) {
		t.Fatalf("ReadHosts with a URL line = %v, want an error naming the file", err)
	}
}
