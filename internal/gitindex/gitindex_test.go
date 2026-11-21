package gitindex

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func gitRepo(t *testing.T) *Repo {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{{"init", "-q"}} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("%v: %s", err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "a b.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", dir, "add", "a b.go").CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	r, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestReadIndexBeforeFirstCommit(t *testing.T) {
	r := gitRepo(t)
	if r.HasHEAD() {
		t.Error("HasHEAD before the first commit")
	}
	blobs, err := r.Read([]string{":a b.go", ":missing.go", "HEAD:a b.go"})
	if err != nil {
		t.Fatal(err)
	}
	if string(blobs[":a b.go"]) != "package a\n" || len(blobs) != 1 {
		t.Errorf("blobs = %q", blobs)
	}
	changed, err := r.ChangedGoFiles("", "")
	if err != nil || len(changed) != 1 || changed[0] != "a b.go" {
		t.Errorf("changed = %q, %v", changed, err)
	}
}

func TestReadRejectsNewlinesAndTrees(t *testing.T) {
	r := gitRepo(t)
	if _, err := r.Read([]string{":a\nb.go"}); err == nil {
		t.Error("no error for a newline in a path")
	}
	blobs, err := r.Read(nil)
	if err != nil || len(blobs) != 0 {
		t.Errorf("Read(nil) = %v, %v", blobs, err)
	}
}

func TestResolveRangeErrors(t *testing.T) {
	r := gitRepo(t)
	for _, spec := range []string{"HEAD", "..HEAD", "a..", "nope..HEAD", "HEAD...nope"} {
		if _, _, err := r.ResolveRange(spec); err == nil {
			t.Errorf("ResolveRange(%q): no error", spec)
		}
	}
}

func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	full := append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false"}, args...)
	if out, err := exec.Command("git", full...).CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

func TestWorktreeAgainstBase(t *testing.T) {
	r := gitRepo(t)
	gitIn(t, r.Root, "commit", "-q", "--no-verify", "-m", "base")
	base, err := r.ResolveBase("HEAD")
	if err != nil || len(base) != 40 {
		t.Fatalf("ResolveBase = %q, %v", base, err)
	}
	if _, err := r.ResolveBase("nope"); err == nil {
		t.Error("ResolveBase(nope): no error")
	}
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(r.Root, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("a b.go", "package a\n\nvar x = 1\n")
	write("new.go", "package a\n")
	write("notes.txt", "x\n")
	if err := os.Symlink("new.go", filepath.Join(r.Root, "link.go")); err != nil {
		t.Fatal(err)
	}

	changed, err := r.ChangedGoFilesInWorktree(base)
	if err != nil || len(changed) != 3 || changed[0] != "a b.go" || changed[1] != "link.go" || changed[2] != "new.go" {
		t.Errorf("changed = %q, %v", changed, err)
	}
	files, err := r.WorktreeFiles("*.go")
	if err != nil || len(files) != 3 {
		t.Errorf("files = %q, %v", files, err)
	}
	blobs, err := r.ReadWorktree([]string{"a b.go", "missing.go", "link.go"})
	if err != nil || len(blobs) != 1 || string(blobs["a b.go"]) != "package a\n\nvar x = 1\n" {
		t.Errorf("blobs = %q, %v", blobs, err)
	}
}
