package gitlab

import "testing"

func TestParseRepositoryNestedGroup(t *testing.T) {
	p, _ := NewProvider(ProviderOptions{Host: "gitlab.example.com", APIBase: "https://gitlab.example.com/api/v4", Token: StaticTokenSource("t"), SkipTokenPreflight: true})
	cases := []string{
		"git@gitlab.example.com:group/sub/proj.git",
		"https://gitlab.example.com/group/sub/proj.git",
		"https://gitlab.example.com/group/sub/proj",
	}
	for _, remote := range cases {
		repo, ok := p.ParseRepository(remote)
		if !ok {
			t.Fatalf("%s: not parsed", remote)
		}
		if repo.Provider != "gitlab" || repo.Host != "gitlab.example.com" {
			t.Fatalf("%s: provider/host = %q/%q", remote, repo.Provider, repo.Host)
		}
		if repo.Repo != "group/sub/proj" || repo.Owner != "group/sub" || repo.Name != "proj" {
			t.Fatalf("%s: repo=%q owner=%q name=%q", remote, repo.Repo, repo.Owner, repo.Name)
		}
	}
}

func TestParseRepositoryRejectsOtherHost(t *testing.T) {
	p, _ := NewProvider(ProviderOptions{Host: "gitlab.example.com", Token: StaticTokenSource("t"), SkipTokenPreflight: true})
	if _, ok := p.ParseRepository("git@github.com:acme/demo.git"); ok {
		t.Fatalf("github.com remote should not be claimed by gitlab provider")
	}
}
