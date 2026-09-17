package standard

import (
	"strings"

	"github.com/lastpersonlabs/toolparser/model"
	"github.com/lastpersonlabs/toolparser/rules"
)

// nodePkgRule covers npm, pnpm, yarn and bun. A script name alone does not
// establish what the script does.
var nodePkgRule = rules.Rule{
	ID: "std/node-package-managers", Version: "1", Executables: []string{"npm", "pnpm", "yarn", "bun", "npx", "bunx", "pnpx"}, Platform: "",
	Doc: "npm <command>: install ci add remove uninstall run test start exec; pnpm/yarn/bun equivalents; npx/bunx run a package binary",
	Apply: func(cmd rules.Command) rules.Result {
		mgr := cmd.Executable.Text
		res := rules.Result{}
		args := cmd.Args
		// Skip leading global flags (--prefix dir, -C dir, --silent ...).
		for len(args) > 0 && args[0].Concrete() && strings.HasPrefix(args[0].Text, "-") {
			if args[0].Text == "--prefix" || args[0].Text == "-C" || args[0].Text == "--cwd" || args[0].Text == "--filter" || args[0].Text == "-F" {
				args = args[1:]
			}
			if len(args) > 0 {
				args = args[1:]
			}
		}
		if mgr == "npx" || mgr == "bunx" || mgr == "pnpx" {
			return runBinary(cmd, mgr, args)
		}
		if len(args) == 0 {
			if mgr == "yarn" || mgr == "pnpm" || mgr == "bun" {
				return install(mgr, nil)
			}
			return res
		}
		if !args[0].Concrete() {
			a := rules.NewAction(model.ProcessUnresolved, model.BasisSyntax)
			a.Process = &model.ProcessDetail{Executable: cmd.Executable, Args: cmd.Args, Reason: "package manager subcommand is not literal"}
			res.Actions = append(res.Actions, a)
			return res
		}
		sub, rest := args[0].Text, args[1:]
		switch sub {
		case "install", "i", "ci", "add", "update", "upgrade", "up", "dedupe", "link":
			return install(mgr, literals(rest))
		case "remove", "rm", "uninstall", "un", "unlink":
			a := rules.NewAction(model.PkgRemove, model.BasisSyntax)
			a.Package = &model.PackageDetail{Manager: mgr, Packages: literals(rest)}
			res.Actions = append(res.Actions, a)
		case "run", "run-script", "test", "t", "start", "build", "lint", "dev":
			script := sub
			if sub == "run" || sub == "run-script" {
				if len(rest) == 0 {
					return res
				}
				script = rest[0].Text
				if !rest[0].Concrete() {
					script = ""
				}
			}
			return runScript(cmd, mgr, script)
		case "exec", "x", "dlx", "create", "init":
			return runBinary(cmd, mgr+" "+sub, rest)
		case "publish":
			a := rules.NewAction(model.NetUpload, model.BasisSyntax)
			a.Network = &model.NetworkDetail{URL: model.Symbolic("<registry>")}
			a.Evidence.Assumptions = []string{"publishes to the configured registry"}
			res.Actions = append(res.Actions, a)
		case "audit", "outdated", "ls", "list", "view", "info", "why", "config", "cache", "version", "help", "--version", "-v":
			a := rules.NewAction(model.ProcessExecute, model.BasisSyntax)
			a.Process = &model.ProcessDetail{Executable: cmd.Executable, Args: cmd.Args}
			a.Evidence.Assumptions = []string{mgr + " " + sub + " has no modeled file effects"}
			res.Actions = append(res.Actions, a)
		default:
			// yarn/pnpm/bun run scripts by bare name.
			if mgr != "npm" {
				return runScript(cmd, mgr, sub)
			}
			a := rules.NewAction(model.ProcessExecute, model.BasisSyntax)
			a.Process = &model.ProcessDetail{Executable: cmd.Executable, Args: cmd.Args}
			a.Evidence.Assumptions = []string{mgr + " subcommand " + sub + " is not modeled"}
			res.Actions = append(res.Actions, a)
		}
		return res
	},
}

func install(mgr string, pkgs []string) rules.Result {
	a := rules.NewAction(model.PkgInstall, model.BasisSyntax)
	a.Package = &model.PackageDetail{Manager: mgr, Packages: pkgs}
	a.Evidence.Assumptions = []string{"installation may run lifecycle scripts and fetch from the network"}
	return rules.Result{Actions: []model.Action{a}}
}

// runScript records a project script invocation. Its contents are unknown
// until snapshot enrichment resolves the package manifest.
func runScript(cmd rules.Command, mgr, script string) rules.Result {
	a := rules.NewAction(model.PkgRunScript, model.BasisSyntax)
	a.Package = &model.PackageDetail{Manager: mgr, Script: script}
	a.Evidence.Assumptions = []string{"the script body is project-defined and not part of the invocation"}
	kind := model.DevBuild
	verifiedName := script
	switch {
	case script == "":
		verifiedName = "<non-literal>"
	case strings.Contains(script, "test"):
		kind = model.DevTest
	case strings.Contains(script, "lint"):
		kind = model.DevLint
	case strings.Contains(script, "format") || strings.Contains(script, "fmt"):
		kind = model.DevFormat
	}
	res := rules.Result{Actions: []model.Action{a}}
	if script != "" {
		i := intent(kind, mgr, verifiedName, false)
		i.Evidence.Assumptions = []string{"intent guessed from the script name; not a verified tool invocation"}
		res.Actions = append(res.Actions, i)
	}
	// The engine resolves the script body from package.json when a snapshot
	// resolver is configured; otherwise it records an unresolved execution.
	res.Script = &rules.Script{Manager: mgr, Name: script, Manifest: "package.json"}
	return res
}

// runBinary hands `npx tool args` to the rule for tool when it is literal.
func runBinary(cmd rules.Command, via string, args []model.Value) rules.Result {
	for len(args) > 0 && args[0].Concrete() && strings.HasPrefix(args[0].Text, "-") {
		if args[0].Text == "-p" || args[0].Text == "--package" || args[0].Text == "-c" || args[0].Text == "--call" {
			args = args[1:]
		}
		if len(args) > 0 {
			args = args[1:]
		}
	}
	if len(args) == 0 {
		return rules.Result{}
	}
	if !args[0].Concrete() {
		a := rules.NewAction(model.ProcessUnresolved, model.BasisSyntax)
		a.Process = &model.ProcessDetail{Executable: cmd.Executable, Args: cmd.Args, Reason: "package binary name is not literal"}
		return rules.Result{Actions: []model.Action{a}}
	}
	// Strip a version suffix (tool@1.2.3) and scope (@scope/tool → tool).
	name := args[0].Text
	if i := strings.LastIndex(name, "@"); i > 0 {
		name = name[:i]
	}
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	exe := model.Value{Knowledge: model.KnowledgeDerived, Text: name, Assumptions: []string{"package binary resolved by " + via + "; may be downloaded first"}, Span: args[0].Span}
	return rules.Result{Wrapped: &rules.Command{Executable: exe, Args: args[1:]}, Assumptions: []string{via + " may install the package before running it"}}
}

var pipRule = rules.Rule{
	ID: "std/pip", Version: "1", Executables: []string{"pip", "pip3", "uv", "poetry", "pipx", "conda", "pipenv"}, Platform: "",
	Doc: "pip <command>: install uninstall download freeze list show; -r requirements; uv pip/add/sync/run; poetry install/add/run",
	Apply: func(cmd rules.Command) rules.Result {
		mgr := cmd.Executable.Text
		res := rules.Result{}
		args := cmd.Args
		if (mgr == "uv" || mgr == "poetry" || mgr == "pipenv") && len(args) > 0 && args[0].Concrete() && args[0].Text == "pip" {
			args = args[1:]
		}
		if len(args) == 0 || !args[0].Concrete() {
			return res
		}
		sub, rest := args[0].Text, args[1:]
		o := rules.ParseOptions(rest, rules.OptionSpec{ShortArgs: "rcite", ShortFlags: "Uuqv", LongArgs: []string{"requirement", "constraint", "index-url", "extra-index-url", "target", "editable", "python", "group", "with", "extra", "package"},
			LongFlags: []string{"upgrade", "user", "no-deps", "no-cache-dir", "quiet", "verbose", "pre", "dev", "all-extras", "frozen", "locked", "system", "yes", "no-install-project", "sync"}})
		switch sub {
		case "install", "add", "sync", "lock", "update", "download", "compile":
			a := rules.NewAction(model.PkgInstall, model.BasisSyntax)
			a.Package = &model.PackageDetail{Manager: mgr, Packages: literals(o.Operands)}
			a.Evidence.Assumptions = append(o.Assumptions, "installation may run setup code and fetch from the network")
			res.Actions = append(res.Actions, a)
			for _, r := range append(o.Values["r"], o.Values["requirement"]...) {
				res.Actions = append(res.Actions, rules.NewAction(model.FSRead, model.BasisSyntax, cmd.Resource(r, "requirements")))
			}
		case "uninstall", "remove":
			a := rules.NewAction(model.PkgRemove, model.BasisSyntax)
			a.Package = &model.PackageDetail{Manager: mgr, Packages: literals(o.Operands)}
			res.Actions = append(res.Actions, a)
		case "run":
			return runBinary(cmd, mgr+" run", rest)
		default:
			a := rules.NewAction(model.ProcessExecute, model.BasisSyntax)
			a.Process = &model.ProcessDetail{Executable: cmd.Executable, Args: cmd.Args}
			a.Evidence.Assumptions = []string{mgr + " subcommand " + sub + " is not modeled"}
			res.Actions = append(res.Actions, a)
		}
		return res
	},
}

var systemPkgRule = rules.Rule{
	ID: "std/system-package-managers", Version: "1", Executables: []string{"apt", "apt-get", "brew", "dnf", "yum", "pacman", "apk", "gem", "composer", "nix-env", "snap"}, Platform: "",
	Doc: "system/global package managers: install/add/remove/uninstall subcommands",
	Apply: func(cmd rules.Command) rules.Result {
		mgr := cmd.Executable.Text
		res := rules.Result{}
		var sub string
		var rest []model.Value
		for i, a := range cmd.Args {
			if a.Concrete() && !strings.HasPrefix(a.Text, "-") {
				sub, rest = a.Text, cmd.Args[i+1:]
				break
			}
		}
		if mgr == "pacman" {
			for _, a := range cmd.Args {
				if a.Concrete() && strings.HasPrefix(a.Text, "-S") {
					sub = "install"
				}
				if a.Concrete() && strings.HasPrefix(a.Text, "-R") {
					sub = "remove"
				}
			}
			rest = cmd.Args
		}
		switch sub {
		case "install", "add", "reinstall", "upgrade", "update", "-i", "require":
			a := rules.NewAction(model.PkgInstall, model.BasisSyntax)
			a.Package = &model.PackageDetail{Manager: mgr, Packages: literals(rest)}
			a.Evidence.Assumptions = []string{"system package installation changes files outside the project"}
			res.Actions = append(res.Actions, a)
		case "remove", "uninstall", "purge", "del", "rm":
			a := rules.NewAction(model.PkgRemove, model.BasisSyntax)
			a.Package = &model.PackageDetail{Manager: mgr, Packages: literals(rest)}
			res.Actions = append(res.Actions, a)
		default:
			a := rules.NewAction(model.ProcessExecute, model.BasisSyntax)
			a.Process = &model.ProcessDetail{Executable: cmd.Executable, Args: cmd.Args}
			a.Evidence.Assumptions = []string{mgr + " subcommand " + sub + " is not modeled"}
			res.Actions = append(res.Actions, a)
		}
		return res
	},
}
