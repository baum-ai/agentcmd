package standard

import (
	"strings"

	"github.com/baum-ai/agentcmd/model"
	"github.com/baum-ai/agentcmd/rules"
)

// grepLike emits one content search per root operand. The pattern is never a
// path; with no roots the scope is the working directory (recursive) or
// stdin.
func grepLike(cmd rules.Command, o rules.Options, patternOpts []string, recursiveDefault bool) rules.Result {
	res := rules.Result{Assumptions: o.Assumptions}
	ops := o.Operands
	var pattern model.Value
	havePattern := false
	for _, name := range patternOpts {
		if v, ok := o.Value(name); ok {
			pattern, havePattern = v, true
		}
	}
	if !havePattern && len(ops) > 0 {
		pattern, ops = ops[0], ops[1:]
		havePattern = true
	}
	if !havePattern {
		return res
	}
	for _, f := range o.Values["f"] {
		res.Actions = append(res.Actions, rules.NewAction(model.FSRead, model.BasisSyntax, cmd.Resource(f, "pattern-file")))
	}
	recursive := recursiveDefault || o.Has("r") || o.Has("R") || o.Has("recursive") || o.Has("dereference-recursive")
	if len(ops) == 0 {
		if !recursive {
			return res // searches stdin
		}
		ops = []model.Value{model.Literal(".")}
	}
	for _, root := range ops {
		a := rules.NewAction(model.SearchContent, model.BasisSyntax, cmd.Resource(root, "scope"))
		a.Search = &model.SearchDetail{Pattern: pattern}
		for _, g := range append(o.Values["include"], o.Values["g"]...) {
			r := cmd.Resource(g, "filter")
			r.Knowledge = model.KnowledgeGlob
			r.Resolved = ""
			a.Resources = append(a.Resources, r)
		}
		a.Evidence.Assumptions = append(a.Evidence.Assumptions, "scope is searched; which files were read is not established")
		res.Actions = append(res.Actions, withAssumptions(a, o))
	}
	return res
}

var grepRule = rules.Rule{
	ID: "std/grep", Version: "1", Executables: []string{"grep", "egrep", "fgrep", "zgrep"}, Platform: "posix",
	Doc: "POSIX grep [-E|-F] [-c|-l|-q] [-insvx] -e pattern_list... [-f pattern_file]... [file...]; GNU -r -R --include --exclude -A -B -C -m --color",
	Apply: func(cmd rules.Command) rules.Result {
		o := rules.ParseOptions(cmd.Args, rules.OptionSpec{ShortFlags: "EFGPcilnqsvxwohHrRaIzZbUuLTy", ShortArgs: "efABCmd",
			LongArgs:  []string{"regexp", "file", "include", "exclude", "exclude-dir", "after-context", "before-context", "context", "max-count", "color", "colour", "binary-files", "devices", "directories", "label"},
			LongFlags: []string{"extended-regexp", "fixed-strings", "basic-regexp", "perl-regexp", "count", "ignore-case", "files-with-matches", "files-without-match", "line-number", "quiet", "silent", "no-messages", "invert-match", "line-regexp", "word-regexp", "only-matching", "with-filename", "no-filename", "recursive", "dereference-recursive", "text", "null", "byte-offset", "no-ignore-case", "initial-tab", "null-data", "line-buffered"}})
		return grepLike(cmd, o, []string{"e", "regexp"}, false)
	},
}

var rgRule = rules.Rule{
	ID: "std/rg", Version: "1", Executables: []string{"rg"}, Platform: "",
	Doc: "ripgrep: rg [OPTIONS] PATTERN [PATH ...]; rg -e PATTERN ...; -g GLOB; -t TYPE; recursive by default",
	Apply: func(cmd rules.Command) rules.Result {
		o := rules.ParseOptions(cmd.Args, rules.OptionSpec{ShortFlags: "aFilLnNoqsSuUvwxzchpH0", ShortArgs: "efgtTABCmMrjdE",
			LongArgs:  []string{"regexp", "file", "glob", "iglob", "type", "type-not", "type-add", "after-context", "before-context", "context", "max-count", "max-columns", "replace", "threads", "max-depth", "encoding", "color", "colors", "sort", "sortr", "pre", "pre-glob", "ignore-file", "engine", "max-filesize", "path-separator", "field-context-separator", "field-match-separator", "context-separator", "dfa-size-limit", "regex-size-limit"},
			LongFlags: []string{"text", "fixed-strings", "ignore-case", "files-with-matches", "files-without-match", "line-number", "no-line-number", "only-matching", "quiet", "case-sensitive", "smart-case", "unrestricted", "multiline", "invert-match", "word-regexp", "line-regexp", "search-zip", "count", "count-matches", "column", "no-column", "heading", "no-heading", "hidden", "no-hidden", "with-filename", "no-filename", "null", "files", "type-list", "json", "vimgrep", "pretty", "trim", "stats", "no-ignore", "no-ignore-vcs", "follow", "no-messages", "debug", "trace", "pcre2", "no-config", "one-file-system", "crlf", "null-data", "passthru", "max-columns-preview", "no-require-git", "sort-files", "line-buffered", "block-buffered", "auto-hybrid-regex"}})
		if o.Has("files") || o.Has("type-list") {
			res := rules.Result{Assumptions: o.Assumptions}
			roots := o.Operands
			if len(roots) == 0 {
				roots = []model.Value{model.Literal(".")}
			}
			for _, r := range roots {
				res.Actions = append(res.Actions, withAssumptions(rules.NewAction(model.SearchPath, model.BasisSyntax, cmd.Resource(r, "scope")), o))
			}
			return res
		}
		return grepLike(cmd, o, []string{"e", "regexp"}, true)
	},
}

// findRule: paths precede the first expression element; -name/-path are
// patterns; -delete deletes matches; -exec/-execdir run a command per match
// with a symbolic {} operand.
var findRule = rules.Rule{
	ID: "std/find", Version: "1", Executables: []string{"find", "gfind"}, Platform: "posix",
	Doc: "POSIX find [-H|-L] path... [operand_expression...]; GNU -delete -exec cmd {} ; -exec cmd {} + -maxdepth -type -name -iname -path -regex -newer -mtime -size -prune -print0",
	Apply: func(cmd rules.Command) rules.Result {
		res := rules.Result{}
		args := cmd.Args
		for len(args) > 0 && args[0].Concrete() && (args[0].Text == "-H" || args[0].Text == "-L" || args[0].Text == "-P" || strings.HasPrefix(args[0].Text, "-O") || args[0].Text == "-D") {
			args = args[1:]
		}
		var roots []model.Value
		for len(args) > 0 && !(args[0].Concrete() && (strings.HasPrefix(args[0].Text, "-") || args[0].Text == "(" || args[0].Text == "!")) {
			roots = append(roots, args[0])
			args = args[1:]
		}
		if len(roots) == 0 {
			roots = []model.Value{model.Literal(".")}
		}
		scope := make([]model.Resource, len(roots))
		for i, r := range roots {
			scope[i] = cmd.Resource(r, "scope")
		}
		search := rules.NewAction(model.SearchPath, model.BasisSyntax, scope...)
		var deleteMatches bool
		var execs [][]model.Value
		withArg := map[string]bool{"-name": true, "-iname": true, "-path": true, "-ipath": true, "-regex": true, "-iregex": true, "-type": true, "-maxdepth": true, "-mindepth": true, "-newer": true, "-mtime": true, "-mmin": true, "-size": true, "-user": true, "-group": true, "-perm": true, "-printf": true, "-fprint": true, "-samefile": true, "-links": true, "-inum": true, "-regextype": true, "-newermt": true, "-ctime": true, "-atime": true, "-not": false}
		for i := 0; i < len(args); i++ {
			a := args[i]
			if !a.Concrete() {
				res.Assumptions = append(res.Assumptions, "non-literal expression element "+a.Text)
				continue
			}
			switch a.Text {
			case "-delete":
				deleteMatches = true
			case "-exec", "-execdir", "-ok", "-okdir":
				var argv []model.Value
				for i++; i < len(args); i++ {
					if args[i].Concrete() && (args[i].Text == ";" || args[i].Text == "+") {
						break
					}
					v := args[i]
					if v.Concrete() && strings.Contains(v.Text, "{}") {
						v = model.Value{Knowledge: model.KnowledgeSymbolic, Text: v.Text, Assumptions: []string{"{} is each matched path"}, Span: v.Span}
					}
					argv = append(argv, v)
				}
				if len(argv) > 0 {
					execs = append(execs, argv)
				}
			case "-name", "-iname", "-path", "-ipath":
				if i+1 < len(args) {
					i++
					r := cmd.Resource(args[i], "filter")
					r.Knowledge, r.Resolved = model.KnowledgeGlob, ""
					search.Resources = append(search.Resources, r)
				}
			default:
				if withArg[a.Text] {
					i++
				}
			}
		}
		search.Evidence.Assumptions = []string{"matches are not enumerated"}
		res.Actions = append(res.Actions, search)
		if deleteMatches {
			for _, s := range scope {
				s.Role = "target"
				d := rules.NewAction(model.FSDelete, model.BasisSyntax, s)
				d.Resources[0].Knowledge = model.KnowledgeGlob
				d.Delete = &model.DeleteDetail{Recursive: true}
				d.Evidence.Assumptions = []string{"deletes every path matching the expression under this root"}
				res.Actions = append(res.Actions, d)
			}
		}
		for _, argv := range execs {
			// One command per match: hand it back as a wrapped command with
			// repetition stated; the engine records find as the wrapper.
			if res.Wrapped == nil {
				res.Wrapped = &rules.Command{Executable: argv[0], Args: argv[1:]}
				res.Assumptions = append(res.Assumptions, "-exec command runs once per match (or once with all matches for +)")
			} else {
				res.Diagnostics = append(res.Diagnostics, model.Diagnostic{Code: model.DiagUnsupported, Severity: model.SeverityInfo, Message: "only the first -exec is expanded"})
			}
		}
		return res
	},
}

var fdRule = rules.Rule{
	ID: "std/fd", Version: "1", Executables: []string{"fd", "fdfind"}, Platform: "",
	Doc: "fd [OPTIONS] [pattern] [path...]; -e EXT -t TYPE -x cmd -X cmd",
	Apply: func(cmd rules.Command) rules.Result {
		o := rules.ParseOptions(cmd.Args, rules.OptionSpec{ShortFlags: "HIiSsgFlLpah0uq", ShortArgs: "detEjcSo", LongArgs: []string{"max-depth", "min-depth", "extension", "type", "exclude", "size", "changed-within", "changed-before", "owner", "threads", "color", "max-results", "base-directory", "path-separator", "search-path", "ignore-file"},
			LongFlags: []string{"hidden", "no-ignore", "no-ignore-vcs", "case-sensitive", "ignore-case", "glob", "regex", "fixed-strings", "absolute-path", "list-details", "follow", "full-path", "print0", "unrestricted", "quiet", "show-errors", "strip-cwd-prefix", "one-file-system"}})
		res := rules.Result{Assumptions: o.Assumptions}
		roots := o.Operands
		if len(roots) > 0 {
			roots = roots[1:] // first operand is the pattern
		}
		if len(roots) == 0 {
			roots = []model.Value{model.Literal(".")}
		}
		for _, r := range roots {
			a := rules.NewAction(model.SearchPath, model.BasisSyntax, cmd.Resource(r, "scope"))
			if len(o.Operands) > 0 {
				a.Search = &model.SearchDetail{Pattern: o.Operands[0]}
			}
			res.Actions = append(res.Actions, withAssumptions(a, o))
		}
		return res
	},
}
