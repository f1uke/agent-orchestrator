package releaseweb

import (
	"path/filepath"
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
