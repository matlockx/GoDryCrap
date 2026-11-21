package analysis

import (
	"go/ast"
	"go/build"
	"go/build/constraint"
	"slices"
	"strings"
)

// Tags is the build environment a file's constraints are evaluated against.
type Tags struct {
	GOOS, GOARCH string
	Cgo          bool
	// Release holds the go1.N tags the toolchain satisfies.
	Release []string
	// Extra holds the -tags values.
	Extra []string
}

// DefaultTags is the environment go build would use here: GOOS, GOARCH,
// CGO_ENABLED and the release tags as go/build reads them, plus extra.
func DefaultTags(extra []string) Tags {
	return Tags{
		GOOS: build.Default.GOOS, GOARCH: build.Default.GOARCH,
		Cgo: build.Default.CgoEnabled, Release: build.Default.ReleaseTags, Extra: extra,
	}
}

var (
	knownOS = []string{
		"aix", "android", "darwin", "dragonfly", "freebsd", "hurd", "illumos", "ios", "js",
		"linux", "nacl", "netbsd", "openbsd", "plan9", "solaris", "wasip1", "windows", "zos",
	}
	knownArch = []string{
		"386", "amd64", "amd64p32", "arm", "armbe", "arm64", "arm64be", "loong64", "mips",
		"mipsle", "mips64", "mips64le", "mips64p32", "mips64p32le", "ppc", "ppc64", "ppc64le",
		"riscv", "riscv64", "s390", "s390x", "sparc", "sparc64", "wasm",
	}
	unixOS = []string{
		"aix", "android", "darwin", "dragonfly", "freebsd", "hurd", "illumos", "ios",
		"linux", "netbsd", "openbsd", "solaris",
	}
)

// satisfied reports whether a single build tag holds. The implied tags follow
// go/build: android implies linux, illumos solaris, ios darwin.
func (t Tags) satisfied(tag string) bool {
	switch {
	case tag == t.GOOS, tag == t.GOARCH, tag == "gc":
		return true
	case tag == "unix":
		return slices.Contains(unixOS, t.GOOS)
	case tag == "cgo":
		return t.Cgo
	case tag == "linux":
		return t.GOOS == "android"
	case tag == "solaris":
		return t.GOOS == "illumos"
	case tag == "darwin":
		return t.GOOS == "ios"
	}
	return slices.Contains(t.Release, tag) || slices.Contains(t.Extra, tag)
}

// nameSuffixes returns the "_"-separated parts after the first "_" of a file's
// base name, without a trailing "test" part: the candidates for a GOOS and
// GOARCH suffix. A name without "_" has none.
func nameSuffixes(path string) []string {
	name := path[strings.LastIndex(path, "/")+1:]
	name, _, _ = strings.Cut(name, ".")
	i := strings.Index(name, "_")
	if i < 0 {
		return nil
	}
	parts := strings.Split(name[i:], "_")
	if n := len(parts); n > 0 && parts[n-1] == "test" {
		parts = parts[:n-1]
	}
	return parts
}

// goodName applies the _GOOS, _GOARCH and _GOOS_GOARCH file name suffix rules
// of go/build, including the _test suffix.
func (t Tags) goodName(path string) bool {
	parts := nameSuffixes(path)
	n := len(parts)
	if n >= 2 && slices.Contains(knownOS, parts[n-2]) && slices.Contains(knownArch, parts[n-1]) {
		return t.satisfied(parts[n-2]) && t.satisfied(parts[n-1])
	}
	if n >= 1 && (slices.Contains(knownOS, parts[n-1]) || slices.Contains(knownArch, parts[n-1])) {
		return t.satisfied(parts[n-1])
	}
	return true
}

// Built reports whether the file is part of a build in environment t: its name
// suffix and its //go:build line both match. A file without constraints is
// always built.
func (f *File) Built(t Tags) bool {
	if !t.goodName(f.Path) {
		return false
	}
	return f.Constraint == nil || f.Constraint.Eval(t.satisfied)
}

// buildConstraint returns the file's //go:build expression, or nil. Only
// comments above the package clause count, as for the go command; a line that
// does not parse is ignored.
func buildConstraint(file *ast.File) constraint.Expr {
	for _, group := range file.Comments {
		if group.Pos() >= file.Package {
			break
		}
		for _, c := range group.List {
			if constraint.IsGoBuild(c.Text) {
				expr, err := constraint.Parse(c.Text)
				if err != nil {
					return nil
				}
				return expr
			}
		}
	}
	return nil
}
