package snapshot

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
)

func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	full := append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false"}, args...)
	if out, err := exec.Command("git", full...).CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

func put(t *testing.T, dir, name, content string) {
	t.Helper()
	full := filepath.Join(dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// diffRepo commits a module with packages p and q and a nested module sub,
// then changes p/a.go and adds p/c.go in the working tree.
func diffRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitIn(t, dir, "init", "-q")
	put(t, dir, "go.mod", "module example.com/m\n")
	put(t, dir, "p/a.go", "package p\n\nfunc A() int { return 1 }\n")
	put(t, dir, "q/b.go", "package q\n\nfunc B() int { return 2 }\n")
	put(t, dir, "sub/go.mod", "module example.com/sub\n")
	put(t, dir, "sub/s.go", "package sub\n\nfunc S() int { return 3 }\n")
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-q", "--no-verify", "-m", "base")
	put(t, dir, "p/a.go", "package p\n\nfunc A() int {\n\tif true {\n\t\treturn 1\n\t}\n\treturn 0\n}\n")
	put(t, dir, "p/c.go", "package p\n\nfunc C() int { return 4 }\n")
	return dir
}

func changedNames(d *Diff) []string {
	var names []string
	for _, c := range d.Changes {
		names = append(names, c.After.Name)
	}
	slices.Sort(names)
	return names
}

func TestLoadDiffWorkingTreeStagedAndRange(t *testing.T) {
	dir := diffRepo(t)
	want := []string{"A", "C"}

	d, err := LoadDiff(dir, "HEAD")
	if err != nil || !slices.Equal(changedNames(d), want) {
		t.Fatalf("working tree: %v, %v", changedNames(d), err)
	}

	gitIn(t, dir, "add", "-A")
	if d, err = LoadDiff(dir, ""); err != nil || !slices.Equal(changedNames(d), want) {
		t.Fatalf("staged: %v, %v", changedNames(d), err)
	}

	gitIn(t, dir, "commit", "-q", "--no-verify", "-m", "head")
	if d, err = LoadDiff(dir, "HEAD~1..HEAD"); err != nil || !slices.Equal(changedNames(d), want) {
		t.Fatalf("range: %v, %v", changedNames(d), err)
	}
	if _, err = LoadDiff(dir, "nope"); err == nil {
		t.Error("LoadDiff(nope): no error")
	}
}

func TestModuleFilesStopAtNestedModule(t *testing.T) {
	d, err := LoadDiff(diffRepo(t), "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	for dir, want := range map[string]string{"p": ".", "q": ".", "sub": "sub", "sub/deep": "sub", ".": "."} {
		if got := d.ModuleRoot(dir); got != want {
			t.Errorf("ModuleRoot(%q) = %q, want %q", dir, got, want)
		}
	}
	paths := func(root string) []string {
		files, err := d.ModuleFiles(root)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, f := range files {
			out = append(out, f.Path)
		}
		slices.Sort(out)
		return out
	}
	if got := paths("."); !slices.Equal(got, []string{"p/a.go", "p/c.go", "q/b.go"}) {
		t.Errorf("root module files = %v", got)
	}
	if got := paths("sub"); !slices.Equal(got, []string{"sub/s.go"}) {
		t.Errorf("sub module files = %v", got)
	}
	pkg, err := d.PackageFiles("p")
	if err != nil || len(pkg) != 2 {
		t.Errorf("PackageFiles(p) = %d files, %v", len(pkg), err)
	}
}
