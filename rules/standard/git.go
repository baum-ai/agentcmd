package standard

import (
	"path"
	"strings"

	"github.com/baum-ai/agentcmd/model"
	"github.com/baum-ai/agentcmd/rules"
)

// gitRule classifies documented git subcommands conservatively. Aliases,
// hooks, config and additional processes remain assumptions.
var gitRule = rules.Rule{
	ID: "std/git", Version: "2", Executables: []string{"git"}, Platform: "",
	Doc: "git [-C path] [-c name=value] [--git-dir=path] [--work-tree=path] [--no-pager] <command> [args]",
	Apply: func(cmd rules.Command) rules.Result {
		o := rules.ParseOptions(cmd.Args, rules.OptionSpec{ShortArgs: "Cc", ShortFlags: "pP", LongArgs: []string{"git-dir", "work-tree", "namespace", "exec-path", "config-env", "list-cmds"},
			LongFlags: []string{"no-pager", "paginate", "bare", "no-replace-objects", "literal-pathspecs", "glob-pathspecs", "noglob-pathspecs", "icase-pathspecs", "no-optional-locks", "version", "help", "html-path", "man-path", "info-path"}, OperandsStopOptions: true})
		res := rules.Result{Assumptions: append(o.Assumptions, "git aliases, hooks and configuration are not modeled")}
		if len(o.Operands) == 0 {
			return res
		}
		sub := o.Operands[0]
		if !sub.Concrete() {
			a := rules.NewAction(model.ProcessUnresolved, model.BasisSyntax)
			a.Process = &model.ProcessDetail{Executable: cmd.Executable, Args: cmd.Args, Reason: "git subcommand is not literal"}
			res.Actions = append(res.Actions, a)
			return res
		}
		repo := cmd.Resource(model.Literal("."), "repository")
		if cs := o.Values["C"]; len(cs) > 0 {
			// Each -C is relative to the previous one, and the command runs
			// there: operands resolve against the composed directory.
			dir := cmd.Cwd
			expr := make([]string, 0, len(cs))
			for _, c := range cs {
				expr = append(expr, c.Text)
				switch {
				case c.Concrete() && strings.HasPrefix(c.Text, "/"):
					dir = model.Value{Knowledge: model.KnowledgeDerived, Text: path.Clean(c.Text), Assumptions: []string{"working directory set by git -C"}}
				case c.Concrete() && dir.Concrete():
					dir = model.Value{Knowledge: model.KnowledgeDerived, Text: path.Join(dir.Text, c.Text), Assumptions: []string{"working directory set by git -C"}}
				default:
					dir = model.Unknown()
				}
			}
			cmd.Cwd = dir
			repo = cmd.Resource(model.Literal("."), "repository")
			repo.Expression = strings.Join(expr, "/")
			if len(cs) == 1 {
				repo.Expression = cs[0].Text
			}
		}
		kind, known := map[string]model.ActionKind{
			"status": model.GitInspect, "log": model.GitInspect, "diff": model.GitInspect, "show": model.GitInspect, "blame": model.GitInspect,
			"rev-parse": model.GitInspect, "ls-files": model.GitInspect, "describe": model.GitInspect, "shortlog": model.GitInspect, "reflog": model.GitInspect,
			"grep": model.GitInspect, "cat-file": model.GitInspect, "ls-tree": model.GitInspect, "rev-list": model.GitInspect, "name-rev": model.GitInspect, "whatchanged": model.GitInspect,
			"add": model.GitStage, "rm": model.GitStage, "mv": model.GitStage,
			"commit": model.GitCommit, "notes": model.GitCommit, "am": model.GitCommit, "apply": model.GitCommit,
			"checkout": model.GitSwitch, "switch": model.GitSwitch, "restore": model.GitSwitch, "worktree": model.GitSwitch,
			"merge": model.GitMerge, "rebase": model.GitMerge, "cherry-pick": model.GitMerge, "revert": model.GitMerge,
			"reset": model.GitReset, "stash": model.GitReset, "clean": model.GitReset,
			"push": model.GitNetwork, "pull": model.GitNetwork, "fetch": model.GitNetwork, "clone": model.GitNetwork, "ls-remote": model.GitNetwork, "submodule": model.GitNetwork,
		}[sub.Text]
		if !known {
			// branch/tag/remote/config list by default and mutate with operands.
			switch sub.Text {
			case "branch", "tag", "remote", "config":
				kind = model.GitInspect
				mutating := false
				for _, r := range o.Rest {
					if r.Concrete() && (r.Text == "-d" || r.Text == "-D" || r.Text == "-m" || r.Text == "add" || r.Text == "remove" || r.Text == "set-url" || r.Text == "--unset" || r.Text == "-a") {
						mutating = true
					}
				}
				if mutating || (sub.Text != "remote" && sub.Text != "config" && hasOperand(o.Rest)) {
					kind = model.GitReset
					res.Assumptions = append(res.Assumptions, sub.Text+" with operands modifies references or configuration")
				}
			default:
				a := rules.NewAction(model.ProcessExecute, model.BasisSyntax, repo)
				a.Process = &model.ProcessDetail{Executable: cmd.Executable, Args: cmd.Args}
				a.Evidence.Assumptions = []string{"git subcommand " + sub.Text + " is not modeled"}
				res.Actions = append(res.Actions, a)
				return res
			}
		}
		a := rules.NewAction(kind, model.BasisSyntax, repo)
		a.Git = &model.GitDetail{Subcommand: sub.Text}
		if kind == model.GitNetwork {
			for _, r := range o.Rest {
				if r.Concrete() && len(r.Text) > 0 && r.Text[0] != '-' {
					if a.Git.Remote == "" {
						a.Git.Remote = r.Text
					} else if a.Git.Ref == "" {
						a.Git.Ref = r.Text
					}
				}
			}
			if sub.Text == "clone" {
				a.Git.Remote, a.Git.Ref = "", ""
				var dest *model.Value
				var url *model.Value
				for i := range o.Rest {
					r := o.Rest[i]
					if r.Concrete() && len(r.Text) > 0 && r.Text[0] == '-' {
						continue
					}
					if url == nil {
						url = &o.Rest[i]
					} else if dest == nil {
						dest = &o.Rest[i]
					}
				}
				if url != nil {
					a.Git.Remote = url.Text
				}
				if dest != nil {
					a.Resources = []model.Resource{cmd.Resource(*dest, "destination")}
				} else {
					a.Resources = nil
					a.Evidence.Assumptions = append(a.Evidence.Assumptions, "clone destination derived from the URL is not computed")
				}
			}
		}
		if kind == model.GitSwitch || sub.Text == "commit" || sub.Text == "merge" || sub.Text == "rebase" {
			for _, r := range o.Rest {
				if r.Concrete() && len(r.Text) > 0 && r.Text[0] != '-' && a.Git.Ref == "" {
					a.Git.Ref = r.Text
				}
			}
			if sub.Text == "commit" {
				a.Git.Ref = ""
			}
		}
		if kind == model.GitStage {
			for _, r := range o.Rest {
				if r.Concrete() && len(r.Text) > 0 && r.Text[0] != '-' {
					a.Resources = append(a.Resources, cmd.Resource(r, "pathspec"))
				}
			}
		}
		res.Actions = append(res.Actions, withAssumptions(a, o))
		return res
	},
}

func hasOperand(vs []model.Value) bool {
	for _, v := range vs {
		if !v.Concrete() || len(v.Text) == 0 || v.Text[0] != '-' {
			return true
		}
	}
	return false
}
