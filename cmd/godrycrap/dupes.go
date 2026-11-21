package main

import (
	"flag"
	"fmt"
	"io"
	"path"
	"sort"
	"strconv"

	"github.com/matlockx/GoDryCrap/internal/analysis"
	"github.com/matlockx/GoDryCrap/internal/dupes"
	"github.com/matlockx/GoDryCrap/internal/gate"
	"github.com/matlockx/GoDryCrap/internal/report"
	"github.com/matlockx/GoDryCrap/internal/snapshot"
	"github.com/matlockx/GoDryCrap/internal/usererr"
)

type dupeSide struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	File      string `json:"file"`
	Line      int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
	Nodes     int    `json:"nodes"`
}

type dupeRow struct {
	Score float64  `json:"score"`
	Left  dupeSide `json:"left"`
	Right dupeSide `json:"right"`
}

// Values of -scope.
const (
	scopePackage = "package"
	scopeModule  = "module"
)

type dupeOptions struct {
	options
	threshold float64
	minLines  int
	minNodes  int
	scope     string
	block     bool
}

// table lists rows, naming in the summary whether a pair blocks.
func (o *dupeOptions) table(rows []dupeRow) report.Table {
	verdict := "advisory"
	if o.block {
		verdict = "blocking"
	}
	table := report.Table{
		Title:   "godrycrap dupes",
		Summary: fmt.Sprintf("%d candidate pairs at or above %v (%s)", len(rows), o.threshold, verdict),
		Columns: []string{"score", "function", "location", "similar to", "location"},
	}
	for _, r := range rows {
		table.Rows = append(table.Rows, []string{
			strconv.FormatFloat(r.Score, 'f', 4, 64),
			r.Left.label(), r.Left.span(),
			r.Right.label(), r.Right.span(),
		})
	}
	return table
}

func runDupes(args []string, stdout, stderr io.Writer) gate.Status {
	fs := flag.NewFlagSet("dupes", flag.ContinueOnError)
	var o dupeOptions
	o.options.register(fs)
	fs.Float64Var(&o.threshold, "threshold", dupes.DefaultThreshold, "minimum Jaccard similarity to report")
	fs.IntVar(&o.minLines, "min-lines", dupes.DefaultMinLines, "minimum source lines in a compared function")
	fs.IntVar(&o.minNodes, "min-nodes", dupes.DefaultMinNodes, "minimum normalized syntax nodes in a compared function")
	fs.StringVar(&o.scope, "scope", scopePackage, "what a changed function is compared with in diff modes: its package, or with `module` every non-test function of its module")
	fs.BoolVar(&o.block, "block", false, "exit 2 when a pair is reported (every reported pair has a changed function)")
	if done, err := parse(fs, &o.options, args, stderr); done || err != nil {
		return parseStatus(stderr, "dupes", err)
	}
	if o.scope != scopePackage && o.scope != scopeModule {
		return parseStatus(stderr, "dupes", usererr.Wrap(fmt.Errorf("unknown -scope %q (want %s or %s)", o.scope, scopePackage, scopeModule), flagHint(fs)))
	}
	rows, err := o.find()
	if err != nil {
		return fail(stderr, "dupes", err)
	}
	table := o.table(rows)
	if err := o.emit(stdout, table, map[string]any{"command": "dupes", "threshold": o.threshold, "scope": o.scope, "pairs": rows}); err != nil {
		return fail(stderr, "dupes", err)
	}
	if o.block && len(rows) > 0 {
		return gate.Fail
	}
	return gate.OK
}

// find compares each function with the others in its pool: every function
// in path mode (per package), or each changed function in diff mode. The
// pool is the changed function's package, or with -scope module its whole
// module. Pools are loaded and released one at a time, and only the
// reported pairs are kept, so memory is bounded by the largest pool.
func (o *dupeOptions) find() ([]dupeRow, error) {
	if len(o.paths) > 0 {
		return o.findInPaths()
	}
	t, err := o.load()
	if err != nil {
		return nil, err
	}
	pools := map[string]bool{}
	for _, c := range t.changes {
		dir := path.Dir(c.After.File)
		if o.scope == scopeModule {
			dir = t.diff.ModuleRoot(dir)
		}
		pools[dir] = true
	}
	rows := []dupeRow{}
	for dir := range pools {
		found, err := o.against(t, dir)
		if err != nil {
			return nil, err
		}
		rows = append(rows, toRows(found)...)
	}
	sortRows(rows)
	return rows, nil
}

func (o *dupeOptions) findInPaths() ([]dupeRow, error) {
	paths, err := snapshot.ListPaths(o.paths)
	if err != nil {
		return nil, err
	}
	rows := []dupeRow{}
	for _, pkg := range paths.Packages {
		files, err := paths.Load(pkg, true)
		if err != nil {
			return nil, err
		}
		rows = append(rows, toRows(dupes.AllPairs(dupes.Entries(files, o.minLines, o.minNodes), o.threshold))...)
	}
	sortRows(rows)
	return rows, nil
}

func toRows(pairs []dupes.Pair) []dupeRow {
	rows := make([]dupeRow, len(pairs))
	for i, p := range pairs {
		rows[i] = dupeRow{Score: dupes.Round(p.Score), Left: side(p.Left), Right: side(p.Right)}
	}
	return rows
}

// sortRows orders rows by score, highest first, then by location.
func sortRows(rows []dupeRow) {
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.Score != b.Score {
			return a.Score > b.Score
		}
		if a.Left.File != b.Left.File {
			return a.Left.File < b.Left.File
		}
		return a.Left.Line < b.Left.Line
	})
}

// against compares the changed functions of one pool with every function
// in it. The pool is the package in dir, or with -scope module the module
// rooted at dir.
func (o *dupeOptions) against(t *target, dir string) ([]dupes.Pair, error) {
	var files []*analysis.File
	var err error
	if o.scope == scopeModule {
		files, err = t.diff.ModuleFiles(dir)
	} else {
		files, err = t.diff.PackageFiles(dir)
	}
	if err != nil {
		return nil, err
	}
	pool := dupes.Entries(files, o.minLines, o.minNodes)
	var mine []*dupes.Entry
	for _, e := range pool {
		if isChanged(t.changes, e.Func) {
			mine = append(mine, e)
		}
	}
	return dupes.Against(mine, pool, o.threshold), nil
}

func isChanged(changes []analysis.Change, fn *analysis.Func) bool {
	for _, c := range changes {
		if c.After.File == fn.File && c.After.Start == fn.Start {
			return true
		}
	}
	return false
}

func side(e *dupes.Entry) dupeSide {
	return dupeSide{Namespace: e.Func.Namespace, Name: e.Func.Name, File: e.Func.File, Line: e.Func.Start, EndLine: e.Func.End, Nodes: e.Nodes}
}

func (s dupeSide) label() string { return s.Namespace + "." + s.Name }

func (s dupeSide) span() string { return fmt.Sprintf("%s:%d-%d", s.File, s.Line, s.EndLine) }
