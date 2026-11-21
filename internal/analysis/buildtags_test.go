package analysis

import "testing"

func TestFileBuilt(t *testing.T) {
	linux := Tags{GOOS: "linux", GOARCH: "arm64", Release: []string{"go1.27"}}
	cases := []struct {
		name, path, src string
		tags            Tags
		want            bool
	}{
		{"plain", "a.go", "package a\n", linux, true},
		{"os suffix matches", "a_linux.go", "package a\n", linux, true},
		{"os suffix differs", "a_windows.go", "package a\n", linux, false},
		{"arch suffix differs", "a_amd64.go", "package a\n", linux, false},
		{"os and arch suffix", "a_linux_arm64.go", "package a\n", linux, true},
		{"os and arch suffix, wrong arch", "a_linux_amd64.go", "package a\n", linux, false},
		{"bare os name is no suffix", "linux.go", "package a\n", linux, true},
		{"unknown suffix", "a_other.go", "package a\n", linux, true},
		{"ignore", "a.go", "//go:build ignore\n\npackage a\n", linux, false},
		{"negated tag", "a.go", "//go:build !race\n\npackage a\n", linux, true},
		{"tag needs -tags", "a.go", "//go:build race\n\npackage a\n", linux, false},
		{"tag from -tags", "a.go", "//go:build race\n\npackage a\n", Tags{GOOS: "linux", Extra: []string{"race"}}, true},
		{"unix", "a.go", "//go:build unix\n\npackage a\n", linux, true},
		{"unix on windows", "a.go", "//go:build unix\n\npackage a\n", Tags{GOOS: "windows"}, false},
		{"cgo off", "a.go", "//go:build cgo\n\npackage a\n", linux, false},
		{"mtp and cgo", "a.go", "//go:build mtp && cgo\n\npackage a\n", Tags{GOOS: "linux", Cgo: true, Extra: []string{"mtp"}}, true},
		{"release tag", "a.go", "//go:build go1.27\n\npackage a\n", linux, true},
		{"android implies linux", "a.go", "//go:build linux\n\npackage a\n", Tags{GOOS: "android"}, true},
		{"comment after package is no constraint", "a.go", "package a\n\n//go:build ignore\n", linux, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, err := ParseFile(tc.path, []byte(tc.src), nil)
			if err != nil {
				t.Fatal(err)
			}
			if got := f.Built(tc.tags); got != tc.want {
				t.Errorf("Built = %v, want %v", got, tc.want)
			}
		})
	}
}
