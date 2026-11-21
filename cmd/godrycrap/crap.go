package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/matlockx/GoDryCrap/internal/analysis"
	"github.com/matlockx/GoDryCrap/internal/coverage"
	"github.com/matlockx/GoDryCrap/internal/gate"
	"github.com/matlockx/GoDryCrap/internal/report"
	"github.com/matlockx/GoDryCrap/internal/usererr"
)

type crapRow struct {
	rowHead
	CC       int     `json:"cc"`
	Coverage float64 `json:"coverage"`
	CRAP     float64 `json:"crap"`
	Ratchet  bool    `json:"ratchet,omitempty"`
	level    gate.Status
}

// profileList is a repeatable -coverprofile flag.
type profileList []string

func (p *profileList) String() string { return strings.Join(*p, ",") }

func (p *profileList) Set(v string) error {
	*p = append(*p, v)
	return nil
}

type crapOptions struct {
	options
	thresholds
	profiles     profileList
	profiledOnly bool
	tags         string
}

func runCrap(args []string, stdout, stderr io.Writer) gate.Status {
	fs := flag.NewFlagSet("crap", flag.ContinueOnError)
	var o crapOptions
	o.options.register(fs)
	o.thresholds.register(fs, "CRAP", "a failing function that already existed and whose CC did not rise warns instead of failing (coverage before the change is unknown)")
	fs.Var(&o.profiles, "coverprofile", "existing go test coverage `profile` to read; repeat to merge shards (required)")
	fs.BoolVar(&o.profiledOnly, "profiled-only", false, "score only the changed files the profile covers; still an error when it covers none")
	fs.StringVar(&o.tags, "tags", "", "comma-separated build `tags`, as for go test -tags; a changed file whose build constraints do not match GOOS, GOARCH and these tags is listed as not built instead of scored")
	if done, err := parse(fs, &o.options, args, stderr); done || err != nil {
		return parseStatus(stderr, "crap", err)
	}
	rows, notBuilt, err := o.score()
	if err != nil {
		return fail(stderr, "crap", err)
	}
	statuses := make([]gate.Status, len(rows))
	table := report.Table{
		Title:   "godrycrap crap",
		Columns: []string{"status", "crap", "cc", "coverage", "function", "location"},
	}
	for i, r := range rows {
		statuses[i] = r.level
		table.Rows = append(table.Rows, []string{r.Status, number(r.CRAP), strconv.Itoa(r.CC), number(r.Coverage) + "%", r.Namespace + "." + r.Name, r.File + ":" + strconv.Itoa(r.Line)})
	}
	table.Summary = counts(statuses)
	if len(notBuilt) > 0 {
		table.Summary += "; not built (not scored): " + strings.Join(notBuilt, ", ")
	}
	if err := o.emit(stdout, table, map[string]any{"command": "crap", "status": gate.Worst(statuses).String(), "functions": rows, "not_built": notBuilt}); err != nil {
		return fail(stderr, "crap", err)
	}
	return gate.Worst(statuses)
}

// score loads the profile and the changed functions and scores each one. The
// second result lists the changed files left out because their build
// constraints do not match.
func (o *crapOptions) score() ([]crapRow, []string, error) {
	profile, err := coverage.Load(o.profiles)
	if err != nil {
		return nil, nil, usererr.Wrap(err, regenerateHint)
	}
	t, err := o.load()
	if err != nil {
		return nil, nil, err
	}
	profile.Bind(t.files)
	blocks, notBuilt, err := o.fileBlocks(profile, t)
	if err != nil {
		return nil, nil, profileError(err)
	}
	rows := []crapRow{}
	for _, c := range t.changes {
		if b, ok := blocks[c.After.File]; ok {
			rows = append(rows, o.row(c, b))
		}
	}
	return rows, notBuilt, nil
}

// fileBlocks looks up the profile's blocks for each file with a changed
// function, and checks that each changed function has its own block.
// A file whose build constraints do not match is returned in notBuilt and
// never looked up: a profile cannot cover it.
// With -profiled-only, files the profile does not cover are left out, but
// it is still an error when it covers none of them.
func (o *crapOptions) fileBlocks(profile *coverage.Profile, t *target) (found map[string][]coverage.Block, notBuilt []string, err error) {
	byFile := map[string][]*analysis.Func{}
	for _, c := range t.changes {
		byFile[c.After.File] = append(byFile[c.After.File], c.After)
	}
	tags := analysis.DefaultTags(strings.FieldsFunc(o.tags, func(r rune) bool { return r == ',' || r == ' ' }))
	found = map[string][]coverage.Block{}
	for _, file := range t.files {
		fns, changed := byFile[file.Path]
		if !changed {
			continue
		}
		if !file.Built(tags) {
			notBuilt = append(notBuilt, file.Path)
			delete(byFile, file.Path)
			continue
		}
		blocks, keep, err := o.lookup(profile, file, fns)
		if err != nil {
			return nil, nil, err
		}
		if keep {
			found[file.Path] = blocks
		}
	}
	if len(byFile) > 0 && len(found) == 0 {
		return nil, nil, fmt.Errorf("%w: none of the %d changed files (wrong shard?)", coverage.ErrNotInProfile, len(byFile))
	}
	return found, notBuilt, nil
}

// lookup is one file's blocks, checked against each changed function in it.
// keep is false for a file the profile does not cover under -profiled-only.
func (o *crapOptions) lookup(profile *coverage.Profile, file *analysis.File, fns []*analysis.Func) (blocks []coverage.Block, keep bool, err error) {
	blocks, err = profile.For(file)
	if errors.Is(err, coverage.ErrNotInProfile) && o.profiledOnly {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	for _, fn := range fns {
		if err := coverage.Check(blocks, fn); err != nil {
			return nil, false, err
		}
	}
	return blocks, true, nil
}

func (o *crapOptions) row(c analysis.Change, blocks []coverage.Block) crapRow {
	fn := c.After
	pct := coverage.Round4(coverage.Percent(blocks, fn.Start, fn.End))
	score := coverage.Round4(coverage.CRAP(fn.CC, pct))
	status := gate.Level(score, o.warn, o.fail)
	excused := status == gate.Fail && o.ratchet && c.Before != nil && fn.CC <= c.Before.CC
	if excused {
		status = gate.Warn
	}
	return crapRow{rowHead: headOf(status, c), CC: fn.CC, Coverage: pct, CRAP: score, Ratchet: excused, level: status}
}

// Hints for coverage profiles that do not fit the source.
const (
	regenerateHint = "write the profile with Go 1.27, e.g. go test -coverprofile=coverage.out ./..., and pass it with -coverprofile coverage.out"
	staleHint      = "the profile is older than the source or from another Go version: rerun go test -coverprofile=coverage.out ./... with Go 1.27 after your last edit"
	shardHint      = "pass the profile of the tests that cover these packages (repeat -coverprofile to merge shards), add -profiled-only to skip files it does not cover, or rerun go test -coverprofile=coverage.out ./..."
)

// profileError marks a profile that does not fit the source as the user's
// to fix.
func profileError(err error) error {
	switch {
	case errors.Is(err, coverage.ErrMismatch):
		return usererr.Wrap(err, staleHint)
	case errors.Is(err, coverage.ErrNotInProfile):
		return usererr.Wrap(err, shardHint)
	}
	return err
}

func number(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}
