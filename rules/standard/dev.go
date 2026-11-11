package standard

import (
	"strings"

	"github.com/baum-ai/agentcmd/model"
	"github.com/baum-ai/agentcmd/rules"
)

// intent builds a development.* action. verified says a known tool was
// invoked directly; an arbitrary project target named "test" is never
// promoted to a verified test run.
func intent(kind model.ActionKind, tool, target string, verified bool, res ...model.Resource) model.Action {
	a := rules.NewAction(kind, model.BasisHeuristic, res...)
	a.Intent = &model.IntentDetail{Tool: tool, Target: target, Verified: verified}
	return a
}

var goRule = rules.Rule{
	ID: "std/go", Version: "1", Executables: []string{"go"}, Platform: "",
	Doc: "go <command> [arguments]: build clean doc env fix fmt generate get install list mod run test tool vet vuln work",
	Apply: func(cmd rules.Command) rules.Result {
		res := rules.Result{}
		if len(cmd.Args) == 0 || !cmd.Args[0].Concrete() {
			return res
		}
		sub, rest := cmd.Args[0].Text, cmd.Args[1:]
		scope := packageScope(cmd, rest)
		switch sub {
		case "test":
			res.Actions = append(res.Actions, intent(model.DevTest, "go", "", true, scope...))
		case "build", "install", "run", "generate":
			a := intent(model.DevBuild, "go", sub, true, scope...)
			if sub == "run" {
				a.Evidence.Assumptions = []string{"go run builds and then executes the program; its effects are unknown"}
				p := rules.NewAction(model.ProcessUnresolved, model.BasisSyntax)
				p.Process = &model.ProcessDetail{Executable: cmd.Executable, Args: cmd.Args, Reason: "compiled program effects are unknown"}
				res.Actions = append(res.Actions, p)
			}
			if sub == "generate" {
				a.Evidence.Assumptions = []string{"go generate runs arbitrary directives from source files"}
			}
			res.Actions = append(res.Actions, a)
		case "vet", "fix":
			res.Actions = append(res.Actions, intent(model.DevLint, "go "+sub, "", true, scope...))
		case "fmt":
			res.Actions = append(res.Actions, intent(model.DevFormat, "gofmt", "", true, scope...))
		case "get":
			a := rules.NewAction(model.PkgInstall, model.BasisSyntax)
			a.Package = &model.PackageDetail{Manager: "go", Packages: literals(rest)}
			res.Actions = append(res.Actions, a)
		case "mod":
			if len(rest) > 0 && rest[0].Concrete() {
				switch rest[0].Text {
				case "download", "tidy", "vendor":
					a := rules.NewAction(model.PkgInstall, model.BasisSyntax)
					a.Package = &model.PackageDetail{Manager: "go", Script: "mod " + rest[0].Text}
					res.Actions = append(res.Actions, a)
				default:
					res.Actions = append(res.Actions, intent(model.DevBuild, "go mod", rest[0].Text, true))
				}
			}
		case "env", "version", "list", "doc", "help", "work", "clean", "tool", "vuln":
			a := rules.NewAction(model.ProcessExecute, model.BasisSyntax)
			a.Process = &model.ProcessDetail{Executable: cmd.Executable, Args: cmd.Args}
			a.Evidence.Assumptions = []string{"go " + sub + " has no modeled file effects"}
			res.Actions = append(res.Actions, a)
		default:
			a := rules.NewAction(model.ProcessExecute, model.BasisSyntax)
			a.Process = &model.ProcessDetail{Executable: cmd.Executable, Args: cmd.Args}
			a.Evidence.Assumptions = []string{"go subcommand " + sub + " is not modeled"}
			res.Actions = append(res.Actions, a)
		}
		return res
	},
}

// packageScope turns Go package patterns into scope resources.
func packageScope(cmd rules.Command, args []model.Value) []model.Resource {
	var out []model.Resource
	for _, a := range args {
		if !a.Concrete() || strings.HasPrefix(a.Text, "-") {
			continue
		}
		if strings.HasPrefix(a.Text, ".") || strings.HasPrefix(a.Text, "/") {
			r := cmd.Resource(a, "scope")
			if strings.Contains(a.Text, "...") {
				r.Knowledge = model.KnowledgeGlob
			}
			out = append(out, r)
		}
	}
	return out
}

func literals(vs []model.Value) []string {
	var out []string
	for _, v := range vs {
		if v.Concrete() && !strings.HasPrefix(v.Text, "-") {
			out = append(out, v.Text)
		}
	}
	return out
}

// makeRule: a target is an intent label only; its recipe is unknown.
var makeRule = rules.Rule{
	ID: "std/make", Version: "1", Executables: []string{"make", "gmake", "just", "task", "mage", "rake", "ninja"}, Platform: "",
	Doc: "make [-f makefile] [-C dir] [-j jobs] [target...]; targets are project-defined",
	Apply: func(cmd rules.Command) rules.Result {
		o := rules.ParseOptions(cmd.Args, rules.OptionSpec{ShortFlags: "BbdeiknpqrRsStvw", ShortArgs: "CfjIloOW", LongArgs: []string{"directory", "file", "makefile", "jobs", "include-dir", "load-average", "old-file", "output-sync", "what-if", "justfile", "taskfile"},
			LongFlags: []string{"always-make", "debug", "environment-overrides", "ignore-errors", "keep-going", "dry-run", "print-data-base", "question", "no-builtin-rules", "silent", "quiet", "no-keep-going", "touch", "version", "print-directory", "list", "summary"}})
		res := rules.Result{Assumptions: append(o.Assumptions, "the target's recipe is project-defined and not analyzed")}
		var dir []model.Resource
		if c, ok := o.Value("C"); ok {
			dir = append(dir, cmd.Resource(c, "scope"))
		}
		targets := o.Operands
		if len(targets) == 0 {
			targets = []model.Value{model.Literal("(default)")}
		}
		for _, t := range targets {
			name := t.Text
			kind := model.DevBuild
			switch {
			case strings.Contains(name, "test") || strings.Contains(name, "check"):
				kind = model.DevTest
			case strings.Contains(name, "lint") || strings.Contains(name, "vet"):
				kind = model.DevLint
			case strings.Contains(name, "fmt") || strings.Contains(name, "format"):
				kind = model.DevFormat
			}
			if !t.Concrete() {
				name = t.Text
			}
			a := intent(kind, cmd.Executable.Text, name, false, dir...)
			a.Evidence.Assumptions = []string{"intent guessed from the target name; not a verified tool invocation"}
			res.Actions = append(res.Actions, a)
			p := rules.NewAction(model.ProcessUnresolved, model.BasisSyntax, dir...)
			p.Process = &model.ProcessDetail{Executable: cmd.Executable, Args: cmd.Args, Reason: "recipe for target " + name + " is not part of the invocation"}
			res.Actions = append(res.Actions, p)
		}
		return res
	},
}

// toolRule builds a rule for a known development tool with a fixed intent.
func toolRule(id string, kind model.ActionKind, exes ...string) rules.Rule {
	return rules.Rule{ID: id, Version: "1", Executables: exes, Doc: "known tool; operands are scope", Apply: func(cmd rules.Command) rules.Result {
		var scope []model.Resource
		for _, a := range cmd.Args {
			if a.Concrete() && !strings.HasPrefix(a.Text, "-") && (strings.ContainsAny(a.Text, "/.") || a.Knowledge == model.KnowledgeGlob) {
				scope = append(scope, cmd.Resource(a, "scope"))
			}
		}
		res := rules.Result{Assumptions: []string{"tool options are not parsed; path-like operands are treated as scope"}}
		a := intent(kind, cmd.Executable.Text, "", true, scope...)
		if kind == model.DevFormat {
			a.Evidence.Assumptions = []string{"formatters may rewrite the files in scope in place"}
		}
		res.Actions = append(res.Actions, a)
		return res
	}}
}

var testRunnerRule = toolRule("std/test-runners", model.DevTest, "pytest", "py.test", "jest", "vitest", "mocha", "ava", "tap", "rspec", "phpunit", "ctest", "gotestsum", "playwright", "cypress", "tox", "nox", "bats")
var linterRule = toolRule("std/linters", model.DevLint, "eslint", "ruff", "flake8", "pylint", "mypy", "pyright", "golangci-lint", "staticcheck", "tsc", "shellcheck", "clippy", "stylelint", "biome", "oxlint", "vet")
var formatterRule = toolRule("std/formatters", model.DevFormat, "prettier", "black", "isort", "gofmt", "goimports", "gofumpt", "rustfmt", "clang-format", "shfmt", "dprint", "autopep8", "yapf")

var cargoRule = rules.Rule{
	ID: "std/cargo", Version: "1", Executables: []string{"cargo"}, Platform: "",
	Doc: "cargo <command>: build check test bench run fmt clippy add remove install update doc clean",
	Apply: func(cmd rules.Command) rules.Result {
		res := rules.Result{}
		if len(cmd.Args) == 0 || !cmd.Args[0].Concrete() {
			return res
		}
		sub := cmd.Args[0].Text
		switch sub {
		case "test", "bench", "nextest":
			res.Actions = append(res.Actions, intent(model.DevTest, "cargo", sub, true))
		case "build", "check", "run", "doc":
			res.Actions = append(res.Actions, intent(model.DevBuild, "cargo", sub, true))
		case "fmt":
			res.Actions = append(res.Actions, intent(model.DevFormat, "cargo fmt", "", true))
		case "clippy":
			res.Actions = append(res.Actions, intent(model.DevLint, "cargo clippy", "", true))
		case "add", "install", "update", "fetch":
			a := rules.NewAction(model.PkgInstall, model.BasisSyntax)
			a.Package = &model.PackageDetail{Manager: "cargo", Packages: literals(cmd.Args[1:])}
			res.Actions = append(res.Actions, a)
		case "remove", "uninstall":
			a := rules.NewAction(model.PkgRemove, model.BasisSyntax)
			a.Package = &model.PackageDetail{Manager: "cargo", Packages: literals(cmd.Args[1:])}
			res.Actions = append(res.Actions, a)
		default:
			a := rules.NewAction(model.ProcessExecute, model.BasisSyntax)
			a.Process = &model.ProcessDetail{Executable: cmd.Executable, Args: cmd.Args}
			a.Evidence.Assumptions = []string{"cargo subcommand " + sub + " is not modeled"}
			res.Actions = append(res.Actions, a)
		}
		return res
	},
}
