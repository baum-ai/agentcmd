package standard

import (
	"strings"

	"github.com/lastpersonlabs/toolparser/model"
	"github.com/lastpersonlabs/toolparser/rules"
)

// wrapper builds a rule that strips a launcher and hands the inner command
// back to the engine, preserving wrapper ancestry.
func wrapper(id, doc string, exes []string, spec rules.OptionSpec, adjust func(rules.Command, rules.Options, *rules.Result)) rules.Rule {
	return rules.Rule{ID: id, Version: "1", Executables: exes, Doc: doc, Apply: func(cmd rules.Command) rules.Result {
		spec.OperandsStopOptions = true
		o := rules.ParseOptions(cmd.Args, spec)
		res := rules.Result{Assumptions: o.Assumptions}
		if adjust != nil {
			adjust(cmd, o, &res)
		}
		if res.Wrapped == nil && res.Program == nil {
			ops := append(append([]model.Value(nil), o.Operands...), o.Rest...)
			if len(ops) == 0 {
				return res
			}
			res.Wrapped = &rules.Command{Executable: ops[0], Args: ops[1:], Env: cmd.Env}
		}
		return res
	}}
}

var envRule = wrapper("std/env", "env [-i] [-u name] [-C dir] [-S string] [name=value]... [utility [argument...]]", []string{"env"},
	rules.OptionSpec{ShortFlags: "iv0", ShortArgs: "uCS", LongArgs: []string{"unset", "chdir", "split-string"}, LongFlags: []string{"ignore-environment", "null"}},
	func(cmd rules.Command, o rules.Options, res *rules.Result) {
		ops := append(append([]model.Value(nil), o.Operands...), o.Rest...)
		env := map[string]model.Value{}
		for k, v := range cmd.Env {
			env[k] = v
		}
		for len(ops) > 0 && ops[0].Concrete() && strings.Contains(ops[0].Text, "=") && !strings.HasPrefix(ops[0].Text, "=") {
			k, v, _ := strings.Cut(ops[0].Text, "=")
			env[k] = model.Literal(v)
			ops = ops[1:]
		}
		if len(ops) == 0 {
			return // prints the environment
		}
		res.Wrapped = &rules.Command{Executable: ops[0], Args: ops[1:], Env: env}
		if d, ok := o.Value("C"); ok {
			res.Cwd = &d
		} else if d, ok := o.Value("chdir"); ok {
			res.Cwd = &d
		}
	})

var timeoutRule = wrapper("std/timeout", "timeout [-k duration] [-s signal] [--foreground] [--preserve-status] [-v] duration command [argument...]", []string{"timeout"},
	rules.OptionSpec{ShortFlags: "v", ShortArgs: "ks", LongArgs: []string{"kill-after", "signal"}, LongFlags: []string{"foreground", "preserve-status", "verbose"}},
	func(cmd rules.Command, o rules.Options, res *rules.Result) {
		ops := append(append([]model.Value(nil), o.Operands...), o.Rest...)
		if len(ops) < 2 {
			return
		}
		res.Assumptions = append(res.Assumptions, "command is killed after "+ops[0].Text+"; it may not complete")
		res.Wrapped = &rules.Command{Executable: ops[1], Args: ops[2:], Env: cmd.Env}
	})

var sudoRule = wrapper("std/sudo", "sudo [-AbEHnPS] [-C num] [-D dir] [-g group] [-h host] [-p prompt] [-R dir] [-T timeout] [-u user] [VAR=value] [-i|-s] [command [arg...]]", []string{"sudo", "doas", "pkexec", "run0"},
	rules.OptionSpec{ShortFlags: "AbEHnPSisvVklK", ShortArgs: "CDghpRTuU", LongArgs: []string{"chdir", "group", "host", "prompt", "chroot", "command-timeout", "user", "other-user", "close-from"}, LongFlags: []string{"askpass", "background", "preserve-env", "set-home", "non-interactive", "preserve-groups", "stdin", "login", "shell", "validate", "version", "list", "remove-timestamp", "reset-timestamp"}},
	func(cmd rules.Command, o rules.Options, res *rules.Result) {
		res.Assumptions = append(res.Assumptions, "runs with elevated privileges")
		ops := append(append([]model.Value(nil), o.Operands...), o.Rest...)
		env := map[string]model.Value{}
		for len(ops) > 0 && ops[0].Concrete() && strings.Contains(ops[0].Text, "=") && !strings.HasPrefix(ops[0].Text, "=") {
			k, v, _ := strings.Cut(ops[0].Text, "=")
			env[k] = model.Literal(v)
			ops = ops[1:]
		}
		if d, ok := o.Value("D"); ok {
			res.Cwd = &d
		}
		if len(ops) == 0 {
			if o.Has("i") || o.Has("s") || o.Has("login") || o.Has("shell") {
				res.Program = &rules.Program{Language: "shell", Interpreter: "sudo", Mode: "stdin", Dialect: "sh"}
				if cmd.Stdin != nil {
					res.Program.Source, res.Program.SourceID = &cmd.Stdin.Value, cmd.Stdin.Source
				}
			}
			return
		}
		if o.Has("s") || o.Has("shell") {
			// sudo -s cmd args: the arguments are joined and run by a shell.
			res.Program = &rules.Program{Language: "shell", Interpreter: "sudo -s", Mode: "inline", Dialect: "sh", Source: joinWords(ops)}
			return
		}
		res.Wrapped = &rules.Command{Executable: ops[0], Args: ops[1:], Env: env}
	})

var niceRule = wrapper("std/nice", "nice [-n increment] utility [argument...]; nohup utility [argument...]; time utility; stdbuf -oL utility; ionice; chrt; unbuffer", []string{"nice", "nohup", "time", "stdbuf", "ionice", "chrt", "unbuffer", "setsid", "chronic", "caffeinate", "systemd-run", "flock", "strace", "ltrace"},
	rules.OptionSpec{ShortFlags: "npvqfcbdmiIrtsxwVuad", ShortArgs: "noeEcp", LongArgs: []string{"adjustment", "output", "error", "input", "class", "classdata", "timeout"}, LongFlags: []string{"portability", "verbose", "quiet", "nonblock", "unlock", "close", "shared", "exclusive", "no-fork", "wait", "pipe", "scope", "same-dir"}}, nil)

var xargsRule = wrapper("std/xargs", "xargs [-0prtx] [-E eof] [-I replstr] [-L number] [-n number] [-P maxprocs] [-s size] [utility [argument...]]", []string{"xargs"},
	rules.OptionSpec{ShortFlags: "0prtxao", ShortArgs: "EIiLnPsdJR", LongArgs: []string{"eof", "replace", "max-lines", "max-args", "max-procs", "max-chars", "delimiter", "arg-file", "process-slot-var"}, LongFlags: []string{"null", "interactive", "verbose", "exit", "no-run-if-empty", "open-tty", "show-limits"}},
	func(cmd rules.Command, o rules.Options, res *rules.Result) {
		ops := append(append([]model.Value(nil), o.Operands...), o.Rest...)
		if len(ops) == 0 {
			ops = []model.Value{model.Literal("echo")}
		}
		items := model.Value{Knowledge: model.KnowledgeSymbolic, Text: "<stdin items>", Assumptions: []string{"arguments come from standard input, one command per batch"}}
		args := ops[1:]
		if repl, ok := o.Value("I"); ok && repl.Concrete() {
			replaced := false
			for i, a := range args {
				if a.Concrete() && strings.Contains(a.Text, repl.Text) {
					args[i] = model.Value{Knowledge: model.KnowledgeSymbolic, Text: a.Text, Assumptions: []string{repl.Text + " is replaced by each stdin item"}, Span: a.Span}
					replaced = true
				}
			}
			if !replaced {
				args = append(args, items)
			}
		} else {
			args = append(append([]model.Value(nil), args...), items)
		}
		res.Assumptions = append(res.Assumptions, "command runs once per batch of stdin items")
		res.Wrapped = &rules.Command{Executable: ops[0], Args: args}
	})

// joinWords renders argv as the command string a remote or nested shell
// would receive. Knowledge is the weakest of the parts.
func joinWords(ops []model.Value) *model.Value {
	var texts []string
	k := model.KnowledgeDerived
	var span *model.SourceSpan
	for i, a := range ops {
		texts = append(texts, a.Text)
		if !a.Concrete() {
			k = model.KnowledgeSymbolic
		}
		if i == 0 {
			span = a.Span
		}
	}
	v := &model.Value{Knowledge: k, Text: strings.Join(texts, " "), Assumptions: []string{"arguments joined with spaces and interpreted by the target shell"}}
	if len(ops) == 1 {
		v.Span = span
	}
	return v
}

var sshRule = rules.Rule{
	ID: "std/ssh", Version: "1", Executables: []string{"ssh", "mosh"}, Platform: "",
	Doc: "ssh [options] destination [command [argument...]]; the command words are joined and run by the remote login shell",
	Apply: func(cmd rules.Command) rules.Result {
		o := rules.ParseOptions(cmd.Args, rules.OptionSpec{ShortFlags: "46AaCfGgKkMNnqsTtVvXxYy", ShortArgs: "BbcDEeFIiJLlmOopQRSWw", OperandsStopOptions: true})
		res := rules.Result{Assumptions: o.Assumptions}
		if len(o.Operands) == 0 {
			return res
		}
		host := o.Operands[0]
		ns := model.Namespace{Kind: model.NamespaceRemote, ID: host.Text}
		if !host.Concrete() {
			ns.ID = ""
		}
		if len(o.Rest) == 0 {
			a := rules.NewAction(model.ProcessShell, model.BasisSyntax)
			a.Process = &model.ProcessDetail{Executable: cmd.Executable, Args: cmd.Args, Shell: true, Reason: "interactive remote session"}
			res.Actions = append(res.Actions, a)
			return res
		}
		res.Program = &rules.Program{Language: "shell", Interpreter: "ssh", Mode: "inline", Dialect: "sh", Source: joinWords(o.Rest), Namespace: &ns}
		res.Assumptions = append(res.Assumptions, "remote login shell assumed POSIX-compatible; remote working directory unknown")
		return res
	},
}

// containerCmd builds a wrapped command in a container namespace.
func containerCmd(id string, ops []model.Value, cwd *model.Value) (*rules.Command, *model.Namespace) {
	if len(ops) == 0 {
		return nil, nil
	}
	ns := &model.Namespace{Kind: model.NamespaceContainer, ID: id}
	return &rules.Command{Executable: ops[0], Args: ops[1:]}, ns
}

var dockerRule = rules.Rule{
	ID: "std/docker", Version: "1", Executables: []string{"docker", "podman", "nerdctl"}, Platform: "",
	Doc: "docker exec [opts] CONTAINER CMD [ARG...]; docker run [opts] IMAGE [CMD] [ARG...]; docker compose exec|run SERVICE CMD; docker cp SRC DEST; docker build PATH",
	Apply: func(cmd rules.Command) rules.Result {
		res := rules.Result{Assumptions: []string{"container filesystem is a separate namespace; mounts are not mapped to host paths"}}
		args := cmd.Args
		for len(args) > 0 && args[0].Concrete() && strings.HasPrefix(args[0].Text, "-") {
			if args[0].Text == "-H" || args[0].Text == "--host" || args[0].Text == "--context" || args[0].Text == "-c" || args[0].Text == "--config" || args[0].Text == "-l" || args[0].Text == "--log-level" {
				args = args[1:]
			}
			if len(args) > 0 {
				args = args[1:]
			}
		}
		if len(args) == 0 || !args[0].Concrete() {
			return res
		}
		sub, rest := args[0].Text, args[1:]
		if sub == "compose" || sub == "container" {
			if len(rest) == 0 || !rest[0].Concrete() {
				return res
			}
			sub, rest = sub+" "+rest[0].Text, rest[1:]
		}
		execSpec := rules.OptionSpec{ShortFlags: "itdT", ShortArgs: "euw", LongArgs: []string{"env", "user", "workdir", "env-file", "detach-keys", "index"}, LongFlags: []string{"interactive", "tty", "detach", "privileged", "no-TTY"}, OperandsStopOptions: true}
		runSpec := rules.OptionSpec{ShortFlags: "itdTPaq", ShortArgs: "vepwuhmlc", LongArgs: []string{"volume", "env", "publish", "workdir", "user", "name", "network", "entrypoint", "mount", "label", "add-host", "cpus", "memory", "platform", "pull", "restart", "env-file", "hostname", "cap-add", "cap-drop", "security-opt", "tmpfs", "device", "shm-size", "log-driver", "log-opt", "ulimit", "health-cmd", "stop-signal", "stop-timeout", "ipc", "pid", "uts", "cgroupns", "expose", "dns", "domainname", "mac-address", "runtime", "storage-opt", "sysctl", "volumes-from", "link", "cidfile", "gpus", "service-ports", "use-aliases"},
			LongFlags: []string{"rm", "detach", "interactive", "tty", "privileged", "init", "read-only", "no-healthcheck", "publish-all", "quiet", "no-deps", "build", "no-build", "service-ports", "no-TTY"}, OperandsStopOptions: true}
		switch sub {
		case "exec", "compose exec", "container exec":
			o := rules.ParseOptions(rest, execSpec)
			res.Assumptions = append(res.Assumptions, o.Assumptions...)
			ops := append(append([]model.Value(nil), o.Operands...), o.Rest...)
			if len(ops) < 2 {
				return res
			}
			res.Wrapped, res.Namespace = containerCmd(ops[0].Text, ops[1:], nil)
			if w, ok := o.Value("w"); ok {
				res.Cwd = &w
			} else if w, ok := o.Value("workdir"); ok {
				res.Cwd = &w
			}
		case "run", "compose run", "container run", "create":
			o := rules.ParseOptions(rest, runSpec)
			res.Assumptions = append(res.Assumptions, o.Assumptions...)
			ops := append(append([]model.Value(nil), o.Operands...), o.Rest...)
			if len(ops) == 0 {
				return res
			}
			image := ops[0].Text
			for _, v := range append(o.Values["v"], o.Values["volume"]...) {
				if v.Concrete() {
					if host, _, ok := strings.Cut(v.Text, ":"); ok {
						r := cmd.Resource(model.Value{Knowledge: model.KnowledgeLiteral, Text: host, Span: v.Span}, "mount")
						a := rules.NewAction(model.FSMetadata, model.BasisSyntax, r)
						a.Evidence.Assumptions = []string{"host path bind-mounted into the container; guest writes to the mount are not mapped back"}
						res.Actions = append(res.Actions, a)
					}
				}
			}
			if _, hasEntry := o.Value("entrypoint"); hasEntry || len(ops) == 1 {
				a := rules.NewAction(model.ProcessUnresolved, model.BasisSyntax)
				a.Process = &model.ProcessDetail{Executable: cmd.Executable, Args: cmd.Args, Reason: "container entrypoint controls the command; effects unknown"}
				res.Actions = append(res.Actions, a)
				return res
			}
			res.Assumptions = append(res.Assumptions, "image entrypoint assumed to execute the given command directly")
			res.Wrapped, res.Namespace = containerCmd(image, ops[1:], nil)
			if w, ok := o.Value("w"); ok {
				res.Cwd = &w
			} else if w, ok := o.Value("workdir"); ok {
				res.Cwd = &w
			}
		case "cp", "container cp", "compose cp":
			var ops []model.Value
			for _, r := range rest {
				if !(r.Concrete() && strings.HasPrefix(r.Text, "-")) {
					ops = append(ops, r)
				}
			}
			if len(ops) != 2 {
				return res
			}
			side := func(v model.Value, role string) model.Resource {
				if c, p, ok := strings.Cut(v.Text, ":"); ok && v.Concrete() && !strings.HasPrefix(v.Text, "/") && !strings.HasPrefix(v.Text, ".") {
					return model.Resource{Role: role, Expression: p, Resolved: cleanAbs(p), Knowledge: model.KnowledgeLiteral, Namespace: model.Namespace{Kind: model.NamespaceContainer, ID: c}, Span: v.Span}
				}
				return cmd.Resource(v, role)
			}
			a := rules.NewAction(model.FSCopy, model.BasisSyntax, side(ops[0], "source"), side(ops[1], "destination"))
			res.Actions = append(res.Actions, a)
		case "build", "compose build", "buildx build", "image build":
			a := intent(model.DevBuild, cmd.Executable.Text+" build", "", true)
			for _, r := range rest {
				if r.Concrete() && !strings.HasPrefix(r.Text, "-") && (r.Text == "." || strings.Contains(r.Text, "/")) {
					a.Resources = append(a.Resources, cmd.Resource(r, "scope"))
				}
			}
			a.Evidence.Assumptions = []string{"the Dockerfile's instructions run inside build containers"}
			res.Actions = append(res.Actions, a)
		case "pull", "push", "login", "logout", "compose pull", "compose push":
			kind := model.NetDownload
			if strings.Contains(sub, "push") {
				kind = model.NetUpload
			}
			if strings.Contains(sub, "log") {
				kind = model.NetRequest
			}
			a := rules.NewAction(kind, model.BasisSyntax)
			a.Network = &model.NetworkDetail{URL: model.Symbolic("<registry>")}
			res.Actions = append(res.Actions, a)
		default:
			a := rules.NewAction(model.ProcessExecute, model.BasisSyntax)
			a.Process = &model.ProcessDetail{Executable: cmd.Executable, Args: cmd.Args}
			a.Evidence.Assumptions = []string{cmd.Executable.Text + " subcommand " + sub + " is not modeled"}
			res.Actions = append(res.Actions, a)
		}
		return res
	},
}

func cleanAbs(p string) string {
	if strings.HasPrefix(p, "/") {
		return rules.Resolve(model.Literal("/"), p)
	}
	return ""
}

var kubectlRule = rules.Rule{
	ID: "std/kubectl", Version: "1", Executables: []string{"kubectl", "oc"}, Platform: "",
	Doc: "kubectl exec [-n ns] [-c container] [-it] POD -- COMMAND [args...]; other subcommands are cluster operations",
	Apply: func(cmd rules.Command) rules.Result {
		res := rules.Result{}
		args := cmd.Args
		if len(args) == 0 || !args[0].Concrete() {
			return res
		}
		if args[0].Text != "exec" {
			a := rules.NewAction(model.NetRequest, model.BasisSyntax)
			a.Network = &model.NetworkDetail{URL: model.Symbolic("<kubernetes api>")}
			a.Evidence.Assumptions = []string{"kubectl " + args[0].Text + " talks to the cluster API; effects are cluster-side"}
			res.Actions = append(res.Actions, a)
			return res
		}
		o := rules.ParseOptions(args[1:], rules.OptionSpec{ShortFlags: "itq", ShortArgs: "ncf", LongArgs: []string{"namespace", "container", "filename", "pod-running-timeout", "context", "kubeconfig"}, LongFlags: []string{"stdin", "tty", "quiet"}})
		res.Assumptions = append(res.Assumptions, o.Assumptions...)
		if len(o.Operands) < 2 {
			return res
		}
		id := "pod/" + o.Operands[0].Text
		if c, ok := o.Value("c"); ok {
			id += "/" + c.Text
		}
		res.Wrapped, res.Namespace = containerCmd(id, o.Operands[1:], nil)
		return res
	},
}
