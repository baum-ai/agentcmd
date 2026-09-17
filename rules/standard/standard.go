// Package standard holds the built-in command rules. Callers select it
// explicitly: toolparser.Config{Rules: standard.Rules()}.
//
// Every rule documents its option grammar and states its boundary: recognizing
// a file operand is not proof that the file was accessed.
package standard

import (
	"github.com/lastpersonlabs/toolparser/model"
	"github.com/lastpersonlabs/toolparser/rules"
)

// Version of the standard ruleset as a whole.
const Version = "0.1.0"

// Rules returns every built-in rule.
func Rules() []rules.Rule {
	return []rules.Rule{
		// reading, listing, search
		catRule, headTailRule, lsRule, wcRule, pagerRule, diffRule, statRule, grepRule, rgRule, findRule, fdRule,
		// modification
		cpRule, mvRule, rmRule, rmdirRule, mkdirRule, touchRule, teeRule, sedRule, lnRule,
		// git, development, packages, network
		gitRule, goRule, makeRule, testRunnerRule, linterRule, formatterRule, cargoRule, nodePkgRule, pipRule, systemPkgRule, curlRule, wgetRule,
		// wrappers and namespaces
		envRule, timeoutRule, sudoRule, niceRule, xargsRule, sshRule, dockerRule, kubectlRule,
		// interpreters
		shellRule, pythonRule, nodeRule, otherInterpreters,
	}
}

// readOperands emits one filesystem.read per file operand; "-" is stdin.
func readOperands(cmd rules.Command, o rules.Options, basis model.Basis) rules.Result {
	res := rules.Result{Assumptions: o.Assumptions}
	for _, op := range o.Operands {
		if op.Text == "-" && op.Concrete() {
			continue
		}
		a := rules.NewAction(model.FSRead, basis, cmd.Resource(op, "target"))
		if len(o.Unknown) > 0 {
			a.Evidence.Assumptions = append(a.Evidence.Assumptions, o.Assumptions...)
		}
		res.Actions = append(res.Actions, a)
	}
	return res
}

var catRule = rules.Rule{
	ID: "std/cat", Version: "1", Executables: []string{"cat"}, Platform: "posix",
	Doc: "POSIX cat: cat [-u] [file...]; GNU adds -A -b -e -E -n -s -t -T -v",
	Apply: func(cmd rules.Command) rules.Result {
		o := rules.ParseOptions(cmd.Args, rules.OptionSpec{ShortFlags: "uAbeEnstTv", LongFlags: []string{"show-all", "number-nonblank", "show-ends", "number", "squeeze-blank", "show-tabs", "show-nonprinting"}})
		return readOperands(cmd, o, model.BasisSyntax)
	},
}

var headTailRule = rules.Rule{
	ID: "std/head-tail", Version: "1", Executables: []string{"head", "tail"}, Platform: "posix",
	Doc: "POSIX head [-n number] [file...]; tail [-f] [-c number|-n number] [file...]; GNU long options",
	Apply: func(cmd rules.Command) rules.Result {
		o := rules.ParseOptions(cmd.Args, rules.OptionSpec{ShortArgs: "nc", ShortFlags: "fFqvzr", LongArgs: []string{"lines", "bytes", "pid", "sleep-interval", "max-unchanged-stats"},
			LongFlags: []string{"follow", "quiet", "silent", "verbose", "zero-terminated", "retry"}})
		return readOperands(cmd, o, model.BasisSyntax)
	},
}

var lsRule = rules.Rule{
	ID: "std/ls", Version: "1", Executables: []string{"ls"}, Platform: "posix",
	Doc: "POSIX ls [-ikqrs] [-glno] [-A|-a] [-C|-m|-x|-1] [-F|-p] [-H|-L] [-R|-d] [-S|-f|-t] [-c|-u] [file...]",
	Apply: func(cmd rules.Command) rules.Result {
		o := rules.ParseOptions(cmd.Args, rules.OptionSpec{ShortFlags: "ikqrsglnoAaCmx1FpHLRdSftcuhGXvUZ", ShortArgs: "IwT",
			LongArgs:  []string{"color", "format", "time-style", "sort", "ignore", "width", "block-size", "hide", "indicator-style", "quoting-style", "time", "tabsize"},
			LongFlags: []string{"all", "almost-all", "human-readable", "recursive", "directory", "reverse", "group-directories-first", "dereference", "classify", "inode", "size", "no-group", "numeric-uid-gid"}})
		res := rules.Result{Assumptions: o.Assumptions}
		if len(o.Operands) == 0 {
			res.Actions = append(res.Actions, rules.NewAction(model.FSList, model.BasisSyntax, cmd.Resource(model.Literal("."), "scope")))
		}
		for _, op := range o.Operands {
			res.Actions = append(res.Actions, rules.NewAction(model.FSList, model.BasisSyntax, cmd.Resource(op, "scope")))
		}
		return res
	},
}
