package standard

import (
	"path"
	"strings"

	"github.com/lastpersonlabs/toolparser/model"
	"github.com/lastpersonlabs/toolparser/rules"
)

// shellRule extracts nested shell programs: sh -c 'string', bash script.sh,
// bash -s with stdin. Option grammar: POSIX sh(1) plus Bash long options.
var shellRule = rules.Rule{
	ID: "std/shell-launcher", Version: "1", Executables: []string{"sh", "bash", "dash", "zsh", "ksh", "mksh", "ash"}, Platform: "posix",
	Doc: "sh [-abCefhimnuvx] [-o option] [-c string [name [arg...]] | -s [arg...] | file [arg...]]; bash adds -l --login --norc --noprofile --posix --rcfile file -O opt",
	Apply: func(cmd rules.Command) rules.Result {
		exe := path.Base(cmd.Executable.Text)
		dialect := map[string]string{"sh": "sh", "dash": "sh", "ash": "sh", "bash": "bash", "zsh": "zsh", "ksh": "mksh", "mksh": "mksh"}[exe]
		o := rules.ParseOptions(cmd.Args, rules.OptionSpec{ShortFlags: "abCefhimnuvxlirspBDEHPT", ShortArgs: "oOc",
			LongFlags: []string{"login", "norc", "noprofile", "posix", "verbose", "version", "help", "noediting", "restricted", "debugger", "dump-strings", "dump-po-strings", "pretty-print"},
			LongArgs:  []string{"rcfile", "init-file"}, OperandsStopOptions: true})
		res := rules.Result{Assumptions: o.Assumptions}
		if src, ok := o.Value("c"); ok {
			// After -c string, the first operand is $0 and the rest are
			// positional parameters.
			var args []model.Value
			if len(o.Operands) > 0 {
				args = o.Rest
			}
			res.Program = &rules.Program{Language: "shell", Interpreter: exe, Mode: "inline", Dialect: dialect, Source: &src, Args: args}
			return res
		}
		if o.Has("c") {
			res.Diagnostics = append(res.Diagnostics, model.Diagnostic{Code: model.DiagUnsupported, Message: "-c without a command string"})
			return res
		}
		if o.Has("s") || len(o.Operands) == 0 {
			res.Program = &rules.Program{Language: "shell", Interpreter: exe, Mode: "stdin", Dialect: dialect, Args: o.Rest}
			if cmd.Stdin != nil {
				res.Program.Source, res.Program.SourceID = &cmd.Stdin.Value, cmd.Stdin.Source
			}
			return res
		}
		file := cmd.Resource(o.Operands[0], "target")
		res.Program = &rules.Program{Language: "shell", Interpreter: exe, Mode: "file", Dialect: dialect, File: &file, Args: o.Rest}
		res.Actions = append(res.Actions, rules.NewAction(model.FSRead, model.BasisSyntax, file))
		return res
	},
}

// pythonRule extracts Python programs. Grammar from the Python command line
// documentation: python [-bBdEhiIOqsSuvVWx?] [-c command | -m module | - |
// script] [args].
var pythonRule = rules.Rule{
	ID: "std/python", Version: "1", Executables: []string{"python", "python3", "python2", "python3.11", "python3.12", "python3.13", "python3.14", "pypy", "pypy3"}, Platform: "",
	Doc: "https://docs.python.org/3/using/cmdline.html",
	Apply: func(cmd rules.Command) rules.Result {
		exe := path.Base(cmd.Executable.Text)
		o := rules.ParseOptions(cmd.Args, rules.OptionSpec{ShortFlags: "bBdEhiIOqsSuvVxPR?", ShortArgs: "WXcm", LongFlags: []string{"help", "version", "help-env", "help-xoptions", "help-all"},
			LongArgs: []string{"check-hash-based-pycs"}, OperandsStopOptions: true})
		res := rules.Result{Assumptions: o.Assumptions}
		// Everything after -c code or -m module is handed to the program.
		progArgs := append(append([]model.Value(nil), o.Operands...), o.Rest...)
		switch {
		case o.Has("c"):
			src, ok := o.Value("c")
			if !ok {
				res.Diagnostics = append(res.Diagnostics, model.Diagnostic{Code: model.DiagUnsupported, Message: "-c without a command string"})
				return res
			}
			res.Program = &rules.Program{Language: "python", Interpreter: exe, Mode: "inline", Source: &src, Args: progArgs}
		case o.Has("m"):
			mod, ok := o.Value("m")
			if !ok {
				return res
			}
			res.Program = &rules.Program{Language: "python", Interpreter: exe, Mode: "module", Args: append([]model.Value{mod}, progArgs...)}
			res.Actions = append(res.Actions, moduleIntent(mod, progArgs)...)
		case len(o.Operands) == 0 || (o.Operands[0].Concrete() && o.Operands[0].Text == "-"):
			res.Program = &rules.Program{Language: "python", Interpreter: exe, Mode: "stdin", Args: o.Rest}
			if cmd.Stdin != nil {
				res.Program.Source, res.Program.SourceID = &cmd.Stdin.Value, cmd.Stdin.Source
			}
		default:
			file := cmd.Resource(o.Operands[0], "target")
			res.Program = &rules.Program{Language: "python", Interpreter: exe, Mode: "file", File: &file, Args: o.Rest}
			res.Actions = append(res.Actions, rules.NewAction(model.FSRead, model.BasisSyntax, file))
		}
		return res
	},
}

// moduleIntent labels well-known Python modules run with -m.
func moduleIntent(mod model.Value, rest []model.Value) []model.Action {
	if !mod.Concrete() {
		return nil
	}
	kind := map[string]model.ActionKind{"pytest": model.DevTest, "unittest": model.DevTest, "pip": model.PkgInstall, "black": model.DevFormat, "ruff": model.DevLint, "mypy": model.DevLint, "flake8": model.DevLint, "pylint": model.DevLint}[mod.Text]
	if kind == "" {
		return nil
	}
	a := rules.NewAction(kind, model.BasisHeuristic)
	if kind == model.PkgInstall {
		a.Package = &model.PackageDetail{Manager: "pip"}
		if len(rest) == 0 || !rest[0].Concrete() || rest[0].Text != "install" {
			a.Kind = model.PkgInstall
			a.Evidence.Assumptions = []string{"pip subcommand not recognized; install is a guess"}
		}
		return []model.Action{a}
	}
	a.Intent = &model.IntentDetail{Tool: mod.Text, Verified: true}
	return []model.Action{a}
}

// nodeRule extracts Node.js programs: node [options] [script.js | -e "script"
// | -p "expr" | -] [arguments]. Grammar from the Node CLI documentation.
var nodeRule = rules.Rule{
	ID: "std/node", Version: "1", Executables: []string{"node", "nodejs"}, Platform: "",
	Doc: "https://nodejs.org/api/cli.html",
	Apply: func(cmd rules.Command) rules.Result {
		exe := path.Base(cmd.Executable.Text)
		o := rules.ParseOptions(cmd.Args, rules.OptionSpec{ShortFlags: "cihv", ShortArgs: "epr",
			LongFlags:           []string{"check", "interactive", "help", "version", "experimental-modules", "no-warnings", "enable-source-maps", "watch", "test", "experimental-strip-types", "experimental-default-type", "no-deprecation", "trace-warnings"},
			LongArgs:            []string{"eval", "print", "require", "import", "input-type", "loader", "experimental-loader", "env-file", "max-old-space-size", "inspect", "inspect-brk", "title", "conditions", "stack-trace-limit"},
			OperandsStopOptions: true})
		res := rules.Result{Assumptions: o.Assumptions}
		for _, name := range []string{"e", "eval", "p", "print"} {
			if src, ok := o.Value(name); ok {
				src := src
				res.Program = &rules.Program{Language: "javascript", Interpreter: exe, Mode: "inline", Source: &src, Args: o.Rest}
				return res
			}
		}
		if len(o.Operands) == 0 || (o.Operands[0].Concrete() && o.Operands[0].Text == "-") {
			if o.Has("v") || o.Has("version") || o.Has("h") || o.Has("help") {
				return res
			}
			res.Program = &rules.Program{Language: "javascript", Interpreter: exe, Mode: "stdin", Args: o.Rest}
			if cmd.Stdin != nil {
				res.Program.Source, res.Program.SourceID = &cmd.Stdin.Value, cmd.Stdin.Source
			}
			return res
		}
		file := cmd.Resource(o.Operands[0], "target")
		res.Program = &rules.Program{Language: "javascript", Interpreter: exe, Mode: "file", File: &file, Args: o.Rest}
		res.Actions = append(res.Actions, rules.NewAction(model.FSRead, model.BasisSyntax, file))
		return res
	},
}

// otherInterpreters preserves programs in languages the analyzer does not
// model, so they surface as unresolved executions rather than vanishing.
var otherInterpreters = rules.Rule{
	ID: "std/other-interpreters", Version: "1", Executables: []string{"perl", "ruby", "awk", "gawk", "mawk", "pwsh", "powershell", "php", "lua", "Rscript", "tclsh", "expect", "osascript", "fish", "tcsh", "csh", "elvish", "nu"},
	Doc: "argv is preserved; option grammars are not parsed",
	Apply: func(cmd rules.Command) rules.Result {
		exe := path.Base(cmd.Executable.Text)
		lang := exe
		if strings.HasPrefix(exe, "power") {
			lang = "powershell"
		}
		res := rules.Result{Assumptions: []string{"interpreter option grammar not parsed; program location unknown"}}
		a := rules.NewAction(model.ProcessUnresolved, model.BasisSyntax)
		a.Process = &model.ProcessDetail{Executable: cmd.Executable, Args: cmd.Args, Reason: lang + " programs are not analyzed"}
		res.Actions = append(res.Actions, a)
		return res
	},
}
