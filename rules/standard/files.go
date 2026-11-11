package standard

import (
	"strings"

	"github.com/baum-ai/agentcmd/model"
	"github.com/baum-ai/agentcmd/rules"
)

func withAssumptions(a model.Action, o rules.Options) model.Action {
	if len(o.Unknown) > 0 {
		a.Evidence.Assumptions = append(a.Evidence.Assumptions, o.Assumptions...)
	}
	return a
}

// cp/mv: the last operand is the destination when there are two or more.
func copyMove(kind model.ActionKind) func(rules.Command) rules.Result {
	return func(cmd rules.Command) rules.Result {
		o := rules.ParseOptions(cmd.Args, rules.OptionSpec{ShortFlags: "abdfHiLlnPpRrsuvxTZ", ShortArgs: "tS",
			LongArgs:  []string{"target-directory", "suffix", "backup", "preserve", "no-preserve", "sparse", "reflink", "context"},
			LongFlags: []string{"archive", "force", "interactive", "link", "dereference", "no-clobber", "no-dereference", "parents", "recursive", "symbolic-link", "update", "verbose", "one-file-system", "no-target-directory", "strip-trailing-slashes", "remove-destination", "copy-contents", "attributes-only"}})
		res := rules.Result{Assumptions: o.Assumptions}
		ops := o.Operands
		var dest model.Value
		hasDest := false
		if t, ok := o.Value("t"); ok {
			dest, hasDest = t, true
		} else if t, ok := o.Value("target-directory"); ok {
			dest, hasDest = t, true
		} else if len(ops) >= 2 {
			dest, ops, hasDest = ops[len(ops)-1], ops[:len(ops)-1], true
		}
		if !hasDest {
			res.Diagnostics = append(res.Diagnostics, model.Diagnostic{Code: model.DiagUnsupported, Severity: model.SeverityInfo, Message: "missing destination operand"})
			return res
		}
		for _, src := range ops {
			a := rules.NewAction(kind, model.BasisSyntax, cmd.Resource(src, "source"), cmd.Resource(dest, "destination"))
			a.Evidence.Assumptions = append(a.Evidence.Assumptions, "destination may be a directory or a file name")
			res.Actions = append(res.Actions, withAssumptions(a, o))
		}
		return res
	}
}

var cpRule = rules.Rule{ID: "std/cp", Version: "1", Executables: []string{"cp"}, Platform: "posix",
	Doc: "POSIX cp [-Pfip] source target; cp [-Pfip] source... target; cp -R [-H|-L|-P] [-fip] source... target; GNU long options", Apply: copyMove(model.FSCopy)}

var mvRule = rules.Rule{ID: "std/mv", Version: "1", Executables: []string{"mv"}, Platform: "posix",
	Doc: "POSIX mv [-if] source target; mv [-if] source... directory; GNU long options", Apply: copyMove(model.FSMove)}

var rmRule = rules.Rule{
	ID: "std/rm", Version: "1", Executables: []string{"rm"}, Platform: "posix",
	Doc: "POSIX rm [-iRr] file...; rm -f [-iRr] [file...]; GNU --recursive --force --dir --one-file-system --preserve-root --no-preserve-root --interactive[=WHEN]",
	Apply: func(cmd rules.Command) rules.Result {
		o := rules.ParseOptions(cmd.Args, rules.OptionSpec{ShortFlags: "iRrfdvI", LongFlags: []string{"recursive", "force", "dir", "one-file-system", "preserve-root", "no-preserve-root", "verbose", "interactive"}})
		res := rules.Result{Assumptions: o.Assumptions}
		d := &model.DeleteDetail{Recursive: o.Has("r") || o.Has("R") || o.Has("recursive"), Force: o.Has("f") || o.Has("force")}
		for _, op := range o.Operands {
			a := rules.NewAction(model.FSDelete, model.BasisSyntax, cmd.Resource(op, "target"))
			a.Delete = d
			a.Evidence.Assumptions = append(a.Evidence.Assumptions, "a requested deletion does not imply the file existed or was removed")
			res.Actions = append(res.Actions, withAssumptions(a, o))
		}
		return res
	},
}

var rmdirRule = rules.Rule{
	ID: "std/rmdir", Version: "1", Executables: []string{"rmdir"}, Platform: "posix", Doc: "POSIX rmdir [-p] dir...",
	Apply: func(cmd rules.Command) rules.Result {
		o := rules.ParseOptions(cmd.Args, rules.OptionSpec{ShortFlags: "pv", LongFlags: []string{"parents", "ignore-fail-on-non-empty", "verbose"}})
		res := rules.Result{Assumptions: o.Assumptions}
		for _, op := range o.Operands {
			a := rules.NewAction(model.FSDelete, model.BasisSyntax, cmd.Resource(op, "target"))
			a.Delete = &model.DeleteDetail{}
			res.Actions = append(res.Actions, withAssumptions(a, o))
		}
		return res
	},
}

var mkdirRule = rules.Rule{
	ID: "std/mkdir", Version: "1", Executables: []string{"mkdir"}, Platform: "posix", Doc: "POSIX mkdir [-p] [-m mode] dir...",
	Apply: func(cmd rules.Command) rules.Result {
		o := rules.ParseOptions(cmd.Args, rules.OptionSpec{ShortFlags: "pvZ", ShortArgs: "m", LongArgs: []string{"mode", "context"}, LongFlags: []string{"parents", "verbose"}})
		res := rules.Result{Assumptions: o.Assumptions}
		mode := model.WriteCreateExclusive
		if o.Has("p") || o.Has("parents") {
			mode = model.WriteUnknown
		}
		for _, op := range o.Operands {
			a := rules.NewAction(model.FSWrite, model.BasisSyntax, cmd.Resource(op, "target"))
			a.Write = &model.WriteDetail{Mode: mode, Directory: true}
			if mode == model.WriteUnknown {
				a.Evidence.Assumptions = append(a.Evidence.Assumptions, "mkdir -p succeeds whether or not the directory exists")
			}
			res.Actions = append(res.Actions, withAssumptions(a, o))
		}
		return res
	},
}

var touchRule = rules.Rule{
	ID: "std/touch", Version: "1", Executables: []string{"touch"}, Platform: "posix", Doc: "POSIX touch [-acm] [-r ref_file | -t time | -d date] file...",
	Apply: func(cmd rules.Command) rules.Result {
		o := rules.ParseOptions(cmd.Args, rules.OptionSpec{ShortFlags: "acmfh", ShortArgs: "rtd", LongArgs: []string{"reference", "date", "time"}, LongFlags: []string{"no-create", "no-dereference"}})
		res := rules.Result{Assumptions: o.Assumptions}
		for _, op := range o.Operands {
			kind := model.FSMetadata
			if !o.Has("c") && !o.Has("no-create") {
				a := rules.NewAction(model.FSWrite, model.BasisSyntax, cmd.Resource(op, "target"))
				a.Write = &model.WriteDetail{Mode: model.WriteUnknown}
				a.Evidence.Assumptions = append(a.Evidence.Assumptions, "creates the file if missing, otherwise only updates timestamps")
				res.Actions = append(res.Actions, withAssumptions(a, o))
				continue
			}
			res.Actions = append(res.Actions, withAssumptions(rules.NewAction(kind, model.BasisSyntax, cmd.Resource(op, "target")), o))
		}
		return res
	},
}

var teeRule = rules.Rule{
	ID: "std/tee", Version: "1", Executables: []string{"tee"}, Platform: "posix", Doc: "POSIX tee [-ai] [file...]",
	Apply: func(cmd rules.Command) rules.Result {
		o := rules.ParseOptions(cmd.Args, rules.OptionSpec{ShortFlags: "aip", LongFlags: []string{"append", "ignore-interrupts"}, LongArgs: []string{"output-error"}})
		res := rules.Result{Assumptions: o.Assumptions}
		mode := model.WriteTruncate
		if o.Has("a") || o.Has("append") {
			mode = model.WriteAppend
		}
		for _, op := range o.Operands {
			a := rules.NewAction(model.FSWrite, model.BasisSyntax, cmd.Resource(op, "target"))
			a.Write = &model.WriteDetail{Mode: mode}
			res.Actions = append(res.Actions, withAssumptions(a, o))
		}
		return res
	},
}

// sed: -i edits operands in place; otherwise operands are read. The first
// non-option operand is the script unless -e/-f was given.
var sedRule = rules.Rule{
	ID: "std/sed", Version: "1", Executables: []string{"sed", "gsed"}, Platform: "posix",
	Doc: "POSIX sed [-n] script [file...]; sed [-n] -e script... [-f script_file]... [file...]; GNU -i[SUFFIX] --in-place[=SUFFIX] -E -r -s -z",
	Apply: func(cmd rules.Command) rules.Result {
		o := rules.ParseOptions(cmd.Args, rules.OptionSpec{ShortFlags: "nErszu", ShortArgs: "efl", LongArgs: []string{"expression", "file", "line-length"},
			LongFlags: []string{"quiet", "silent", "regexp-extended", "separate", "null-data", "unbuffered", "debug", "posix", "sandbox", "follow-symlinks"}})
		res := rules.Result{Assumptions: o.Assumptions}
		// -i takes an optional attached suffix; treat any -i* unknown option as in-place.
		inPlace := false
		var unknown []string
		for _, u := range o.Unknown {
			if strings.HasPrefix(u, "-i") || strings.HasPrefix(u, "--in-place") {
				inPlace = true
				continue
			}
			unknown = append(unknown, u)
		}
		o.Unknown = unknown
		for _, f := range o.Values["f"] {
			res.Actions = append(res.Actions, rules.NewAction(model.FSRead, model.BasisSyntax, cmd.Resource(f, "script")))
		}
		files := o.Operands
		if len(o.Values["e"]) == 0 && len(o.Values["f"]) == 0 && len(files) > 0 {
			files = files[1:] // first operand is the script
		}
		for _, f := range files {
			if inPlace {
				a := rules.NewAction(model.FSWrite, model.BasisSyntax, cmd.Resource(f, "target"))
				a.Write = &model.WriteDetail{Mode: model.WriteInPlace}
				res.Actions = append(res.Actions, withAssumptions(a, o))
				continue
			}
			res.Actions = append(res.Actions, withAssumptions(rules.NewAction(model.FSRead, model.BasisSyntax, cmd.Resource(f, "target")), o))
		}
		return res
	},
}

// readerRule builds a rule for commands that only read their file operands.
func readerRule(id string, exes []string, doc string, spec rules.OptionSpec) rules.Rule {
	return rules.Rule{ID: id, Version: "1", Executables: exes, Platform: "posix", Doc: doc,
		Apply: func(cmd rules.Command) rules.Result {
			return readOperands(cmd, rules.ParseOptions(cmd.Args, spec), model.BasisSyntax)
		}}
}

var wcRule = readerRule("std/wc", []string{"wc"}, "POSIX wc [-c|-m] [-lw] [file...]", rules.OptionSpec{ShortFlags: "clmwL", LongFlags: []string{"bytes", "chars", "lines", "words", "max-line-length"}, LongArgs: []string{"files0-from"}})
var pagerRule = readerRule("std/pager", []string{"less", "more", "bat", "batcat", "view"}, "pagers read their operands", rules.OptionSpec{ShortFlags: "NRSFXpn", ShortArgs: "l", LongFlags: []string{"plain", "paging", "number"}, LongArgs: []string{"style", "language", "theme", "line-range", "paging"}})
var diffRule = readerRule("std/diff", []string{"diff", "cmp", "sort", "uniq", "tr", "cut", "awkless", "nl", "od", "xxd", "hexdump", "strings", "file", "md5sum", "sha1sum", "sha256sum", "shasum", "jq", "yq"},
	"text utilities read their file operands; options with arguments are declared per POSIX where common", rules.OptionSpec{ShortFlags: "rqubwBiNaycsnkfmodtERSlLpvex", ShortArgs: "UCIDF", LongFlags: []string{"recursive", "brief", "color", "unified", "raw-output", "compact-output", "sort-keys", "exit-status", "check", "ignore-case", "reverse", "numeric-sort", "unique", "count"}, LongArgs: []string{"exclude", "ignore-matching-lines", "arg", "argjson", "slurpfile", "rawfile", "key", "field-separator", "indent"}})

var statRule = rules.Rule{
	ID: "std/stat", Version: "1", Executables: []string{"stat", "chmod", "chown", "chgrp", "du", "readlink", "realpath", "basename", "dirname", "test", "["}, Platform: "posix",
	Doc: "metadata commands: operands are inspected or have their metadata changed; chmod/chown/chgrp first operand is the mode/owner",
	Apply: func(cmd rules.Command) rules.Result {
		exe := cmd.Executable.Text
		o := rules.ParseOptions(cmd.Args, rules.OptionSpec{ShortFlags: "LfthRvcsaHPxzqmnEe", ShortArgs: "BdX", LongArgs: []string{"format", "printf", "reference", "block-size", "max-depth", "exclude", "threshold"}, LongFlags: []string{"dereference", "terse", "recursive", "verbose", "changes", "summarize", "human-readable", "all", "total", "canonicalize", "quiet", "zero", "no-preserve-root", "preserve-root"}})
		res := rules.Result{Assumptions: o.Assumptions}
		ops := o.Operands
		if (exe == "chmod" || exe == "chown" || exe == "chgrp") && len(ops) > 0 {
			ops = ops[1:]
		}
		if exe == "basename" || exe == "dirname" {
			return res // pure string operations
		}
		for _, op := range ops {
			if exe == "test" || exe == "[" {
				if !op.Concrete() || strings.HasPrefix(op.Text, "-") || op.Text == "]" {
					continue
				}
			}
			res.Actions = append(res.Actions, withAssumptions(rules.NewAction(model.FSMetadata, model.BasisSyntax, cmd.Resource(op, "target")), o))
		}
		return res
	},
}

var lnRule = rules.Rule{
	ID: "std/ln", Version: "1", Executables: []string{"ln"}, Platform: "posix", Doc: "POSIX ln [-fs] [-L|-P] source_file target_file; ln [-fs] source_file... target_dir",
	Apply: func(cmd rules.Command) rules.Result {
		o := rules.ParseOptions(cmd.Args, rules.OptionSpec{ShortFlags: "fsLPnvir", ShortArgs: "tS", LongArgs: []string{"target-directory", "suffix", "backup"}, LongFlags: []string{"force", "symbolic", "logical", "physical", "no-dereference", "verbose", "interactive", "relative", "no-target-directory"}})
		res := rules.Result{Assumptions: o.Assumptions}
		if len(o.Operands) < 2 {
			return res
		}
		dest := o.Operands[len(o.Operands)-1]
		for _, src := range o.Operands[:len(o.Operands)-1] {
			a := rules.NewAction(model.FSWrite, model.BasisSyntax, cmd.Resource(dest, "target"), cmd.Resource(src, "source"))
			a.Write = &model.WriteDetail{Mode: model.WriteCreateExclusive}
			a.Evidence.Assumptions = append(a.Evidence.Assumptions, "creates a link at the target; -f would replace an existing one")
			res.Actions = append(res.Actions, withAssumptions(a, o))
		}
		return res
	},
}
