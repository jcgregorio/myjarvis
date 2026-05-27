package property

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/jcgregorio/myjarvis/internal/obsidian"
)

func TestResolveDate(t *testing.T) {
	today, err := time.Parse("2006-01-02", "2026-05-21")
	assert.NoError(t, err)
	today = today.UTC()
	tests := []struct {
		in   string
		want time.Time
	}{
		{"", today},
		{"today", today},
		{"Today", today},
		{"yesterday", today.AddDate(0, 0, -1)},
		{"3 days ago", today.AddDate(0, 0, -3)},
		{"1 day ago", today.AddDate(0, 0, -1)},
		{"next tuesday", today}, // unrecognized → today
	}
	for _, tt := range tests {
		if got := resolveDate(today, tt.in); got != tt.want {
			t.Errorf("resolveDate(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestAppendUnderLogSection(t *testing.T) {
	tmp := t.TempDir()

	p1 := filepath.Join(tmp, "a.md")
	os.WriteFile(p1, []byte("# 5 Myrtle Ct\n\nNice place.\n"), 0644)
	if err := appendUnderLogSection(p1, "2026-05-18, 24, Removed personal items"); err != nil {
		t.Fatal(err)
	}
	want1 := "# 5 Myrtle Ct\n\nNice place.\n\n## Log\n2026-05-18, 24, Removed personal items\n"
	if got, _ := os.ReadFile(p1); string(got) != want1 {
		t.Errorf("create section:\n got %q\nwant %q", got, want1)
	}

	if err := appendUnderLogSection(p1, "2026-05-19, 0, Rick replaced the lock"); err != nil {
		t.Fatal(err)
	}
	want2 := want1 + "2026-05-19, 0, Rick replaced the lock\n"
	if got, _ := os.ReadFile(p1); string(got) != want2 {
		t.Errorf("append existing:\n got %q\nwant %q", got, want2)
	}

	p3 := filepath.Join(tmp, "b.md")
	os.WriteFile(p3, []byte("## Log\n2026-01-01, 8, old\n\n## Notes\nkeep me\n"), 0644)
	if err := appendUnderLogSection(p3, "2026-05-18, 8, new"); err != nil {
		t.Fatal(err)
	}
	want3 := "## Log\n2026-01-01, 8, old\n2026-05-18, 8, new\n\n## Notes\nkeep me\n"
	if got, _ := os.ReadFile(p3); string(got) != want3 {
		t.Errorf("insert before next heading:\n got %q\nwant %q", got, want3)
	}
}

func TestResolveProperty(t *testing.T) {
	tmp := t.TempDir()
	orig := dir
	dir = tmp
	defer func() { dir = orig }()

	for _, n := range []string{
		"5 Myrtle Ct. Ocean Isle Beach 28469",
		"0 Hayes Run Rd. New Hill NC",
	} {
		os.WriteFile(filepath.Join(tmp, n+".md"), []byte("x"), 0644)
	}

	cases := []struct {
		in     string
		want   string
		wantOK bool
	}{
		{"5 Myrtle Ct. Ocean Isle Beach 28469", "5 Myrtle Ct. Ocean Isle Beach 28469", true},
		{"0 hayes run rd. new hill nc", "0 Hayes Run Rd. New Hill NC", true},
		{"myrtle", "5 Myrtle Ct. Ocean Isle Beach 28469", true},
		{"hayes run", "0 Hayes Run Rd. New Hill NC", true},
		{"n", "", false},
		{"WindJammer", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		got, ok := resolveProperty(c.in)
		if got != c.want || ok != c.wantOK {
			t.Errorf("resolveProperty(%q) = (%q, %v), want (%q, %v)", c.in, got, ok, c.want, c.wantOK)
		}
	}
}

func Test_dateToTTS(t *testing.T) {
	today, err := time.Parse("2006-01-02", "2026-05-21")
	assert.NoError(t, err)
	assert.Equal(t, "May 21", dateToTTS(today))
}

func Test_dateToMarkdown(t *testing.T) {
	today, err := time.Parse("2006-01-02", "2026-05-21")
	assert.NoError(t, err)
	assert.Equal(t, "2026-05-21", dateToMarkdown(today))
}

func TestShortProp(t *testing.T) {
	tests := []struct{ in, want string }{
		{"5 Myrtle Ct. Ocean Isle Beach 28469", "5 Myrtle Ct"},
		{"0 Hayes Run Rd. New Hill NC", "0 Hayes Run Rd"},
		{"Simple Name", "Simple Name"},
		{"Foo, Bar", "Foo"},
		{"", ""},
		{".leadingDot", ".leadingDot"},
		{"Trailing dot.", "Trailing dot"},
	}
	for _, tt := range tests {
		assert.Equalf(t, tt.want, shortProp(tt.in), "shortProp(%q)", tt.in)
	}
}

// setupGitObsidian creates a temp obsidian-style repo with a bare remote
// and overrides obsidian.Repo + property.dir so Log's git pull/commit/push
// actually work in tests. Restored via t.Cleanup. Returns the repo path.
func setupGitObsidian(t *testing.T) string {
	t.Helper()
	base := t.TempDir()
	remote := filepath.Join(base, "remote.git")
	repo := filepath.Join(base, "repo")

	run := func(d, name string, args ...string) {
		cmd := exec.Command(name, args...)
		if d != "" {
			cmd.Dir = d
		}
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s %v: %v\n%s", name, args, err, out)
		}
	}
	run("", "git", "init", "--bare", remote)
	run("", "git", "clone", remote, repo)
	run(repo, "git", "config", "user.email", "test@example.com")
	run(repo, "git", "config", "user.name", "Test")
	if err := os.MkdirAll(filepath.Join(repo, "Properties"), 0o755); err != nil {
		t.Fatal(err)
	}
	run(repo, "git", "commit", "--allow-empty", "-m", "init")
	run(repo, "git", "push", "-u", "origin", "HEAD")

	origRepo := obsidian.Repo()
	origDir := dir
	obsidian.SetRepo(repo)
	dir = filepath.Join(repo, "Properties")
	t.Cleanup(func() {
		obsidian.SetRepo(origRepo)
		dir = origDir
	})
	return repo
}

func TestLog_EmptyDescription(t *testing.T) {
	resp, err := Log("anything", 0, "   ", "today")
	assert.Error(t, err)
	assert.Empty(t, resp)
}

func TestLog_UnknownProperty(t *testing.T) {
	tmp := t.TempDir()
	orig := dir
	dir = tmp
	t.Cleanup(func() { dir = orig })

	resp, err := Log("WindJammer", 0, "Rick replaced the lock", "today")
	assert.NoError(t, err)
	assert.Equal(t, "I don't have a property page for WindJammer.", resp)
}

func TestLog_HappyPath(t *testing.T) {
	repo := setupGitObsidian(t)
	page := "5 Myrtle Ct. Ocean Isle Beach 28469"
	pagePath := filepath.Join(repo, "Properties", page+".md")
	if err := os.WriteFile(pagePath, []byte("# 5 Myrtle Ct\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	resp, err := Log(page, 24, "Removed personal items", "today")
	assert.NoError(t, err)
	assert.Contains(t, resp, "5 Myrtle Ct")
	assert.Contains(t, resp, "24 hours")
	assert.Contains(t, resp, "Removed personal items")

	content, err := os.ReadFile(pagePath)
	assert.NoError(t, err)
	assert.Contains(t, string(content), "## Log")
	assert.Contains(t, string(content), ", 24, Removed personal items")
}

func TestLog_NegativeHoursClamped(t *testing.T) {
	repo := setupGitObsidian(t)
	page := "0 Hayes Run Rd. New Hill NC"
	pagePath := filepath.Join(repo, "Properties", page+".md")
	if err := os.WriteFile(pagePath, []byte("# Hayes Run\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	resp, err := Log(page, -5, "Rick replaced the lock", "today")
	assert.NoError(t, err)
	assert.Contains(t, resp, "0 hours")

	content, _ := os.ReadFile(pagePath)
	assert.Contains(t, string(content), ", 0, Rick replaced the lock")
	assert.NotContains(t, string(content), ", -5,")
}

func TestLog_OneHourSingular(t *testing.T) {
	repo := setupGitObsidian(t)
	page := "5 Myrtle Ct. Ocean Isle Beach 28469"
	pagePath := filepath.Join(repo, "Properties", page+".md")
	if err := os.WriteFile(pagePath, []byte("# Myrtle\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	resp, err := Log(page, 1, "Walkthrough", "today")
	assert.NoError(t, err)
	assert.Contains(t, resp, "1 hour, ")
	assert.NotContains(t, resp, "1 hours")
}
