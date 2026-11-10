package languages

import (
	"strings"

	ts "github.com/odvcencio/gotreesitter"

	"github.com/baum-ai/agentcmd/model"
)

// pyFrontend implements the Python subset: imports, simple assignments,
// string/path construction, pathlib/os/shutil/open/subprocess calls, and
// if/for/while/with/try qualification. Function bodies are latent until the
// function is called at module level.
type pyFrontend struct{ *frontend }

// pyLibraries are the library roots the analyzer recognizes on import.
var pyLibraries = map[string]bool{"os": true, "os.path": true, "pathlib": true, "shutil": true, "subprocess": true, "sys": true, "io": true, "glob": true, "tempfile": true, "json": true}

func (f *pyFrontend) module(parent string, root *ts.Node, fl flow) {
	f.scope.set("open", &binding{kind: bindImport, qualified: "builtins.open"})
	f.block(parent, root, fl)
}

func (f *pyFrontend) block(parent string, n *ts.Node, fl flow) {
	for _, st := range f.named(n) {
		if !f.step(parent, st, fl) {
			return
		}
		f.stmt(parent, st, fl)
	}
}

func (f *pyFrontend) stmt(parent string, n *ts.Node, fl flow) {
	switch f.typ(n) {
	case "comment", "pass_statement", "break_statement", "continue_statement", "global_statement", "nonlocal_statement", "import_future_statement":
	case "expression_statement":
		for _, e := range f.named(n) {
			f.eval(parent, e, fl)
		}
	case "import_statement":
		for _, item := range f.named(n) {
			f.importName(item)
		}
	case "import_from_statement":
		f.importFrom(n)
	case "assignment", "augmented_assignment":
		f.assign(parent, n, fl)
	case "with_statement":
		id := f.container(parent, n, model.NodeGroup, "with", fl)
		for _, clause := range f.named(n) {
			if f.typ(clause) != "with_clause" {
				continue
			}
			for _, item := range f.named(clause) {
				v := f.field(item, "value")
				if v == nil {
					continue
				}
				if f.typ(v) == "as_pattern" {
					val := f.eval(id, v.NamedChild(0), fl.with(fl.reach))
					if alias := f.field(v, "alias"); alias != nil {
						f.scope.set(f.text(alias), &binding{kind: bindValue, value: val, isPath: false})
					}
					continue
				}
				f.eval(id, v, fl.with(fl.reach))
			}
		}
		if body := f.field(n, "body"); body != nil {
			f.block(id, body, fl.with(fl.reach))
		}
	case "if_statement":
		id := f.container(parent, n, model.NodeIf, "if", fl)
		f.ifChain(id, n, fl.with(fl.reach))
	case "for_statement":
		id := f.container(parent, n, model.NodeLoop, "for", fl)
		var elemIsPath bool
		if right := f.field(n, "right"); right != nil {
			_, elemIsPath = f.evalPath(id, right, fl.with(fl.reach))
		}
		if left := f.field(n, "left"); left != nil && f.typ(left) == "identifier" {
			f.scope.set(f.text(left), &binding{kind: bindValue, value: symbolic(f.text(left), "loop variable over "+f.text(f.field(n, "right"))), isPath: elemIsPath})
		}
		if body := f.field(n, "body"); body != nil {
			f.block(id, body, fl.with(model.ReachRepeated))
		}
	case "while_statement":
		id := f.container(parent, n, model.NodeLoop, "while", fl)
		if body := f.field(n, "body"); body != nil {
			f.block(id, body, fl.with(model.ReachRepeated))
		}
	case "try_statement":
		id := f.container(parent, n, model.NodeGroup, "try", fl)
		if body := f.field(n, "body"); body != nil {
			f.block(id, body, fl.with(fl.reach))
		}
		for _, c := range f.named(n) {
			switch f.typ(c) {
			case "except_clause", "except_group_clause", "else_clause":
				for _, b := range f.named(c) {
					if f.typ(b) == "block" {
						f.block(id, b, fl.with(model.ReachConditional))
					}
				}
			case "finally_clause":
				for _, b := range f.named(c) {
					if f.typ(b) == "block" {
						f.block(id, b, fl.with(fl.reach))
					}
				}
			}
		}
	case "function_definition":
		name := f.field(n, "name")
		id := f.container(parent, n, model.NodeFunction, f.text(name), fl)
		_ = id
		f.scope.set(f.text(name), &binding{kind: bindFunc, fn: n, shadowed: pyLibraries[f.text(name)] || f.text(name) == "open"})
	case "decorated_definition":
		for _, c := range f.named(n) {
			if f.typ(c) == "function_definition" || f.typ(c) == "class_definition" {
				f.stmt(parent, c, fl)
			}
		}
	case "class_definition":
		name := f.field(n, "name")
		f.container(parent, n, model.NodeFunction, "class "+f.text(name), fl)
		f.scope.set(f.text(name), &binding{kind: bindOpaque})
	case "return_statement", "delete_statement", "assert_statement", "raise_statement", "print_statement", "exec_statement":
		for _, e := range f.named(n) {
			f.eval(parent, e, fl)
		}
	case "ERROR":
		f.unknown(parent, n, fl, "python syntax error region")
	default:
		// Expressions used as statements (the runtime collapses
		// expression_statement) and anything else.
		if strings.HasSuffix(f.typ(n), "_statement") || strings.HasSuffix(f.typ(n), "_definition") {
			f.unknown(parent, n, fl, "python "+f.typ(n)+" is not modeled")
			return
		}
		f.eval(parent, n, fl)
	}
}

func (f *pyFrontend) ifChain(parent string, n *ts.Node, fl flow) {
	if cond := f.field(n, "condition"); cond != nil {
		f.eval(parent, cond, fl)
	}
	if cons := f.field(n, "consequence"); cons != nil {
		f.block(parent, cons, fl.with(model.ReachConditional))
	}
	for _, alt := range f.named(n) {
		switch f.typ(alt) {
		case "elif_clause":
			f.ifChain(parent, alt, fl)
		case "else_clause":
			if body := f.field(alt, "body"); body != nil {
				f.block(parent, body, fl.with(model.ReachConditional))
			}
		}
	}
}

func (f *pyFrontend) importName(item *ts.Node) {
	switch f.typ(item) {
	case "dotted_name":
		name := f.text(item)
		root := strings.SplitN(name, ".", 2)[0]
		f.bindImport(root, root)
	case "aliased_import":
		name := f.text(f.field(item, "name"))
		alias := f.text(f.field(item, "alias"))
		f.bindImport(alias, name)
	}
}

func (f *pyFrontend) importFrom(n *ts.Node) {
	mod := f.field(n, "module_name")
	if mod == nil {
		return
	}
	module := f.text(mod)
	for _, item := range f.named(n) {
		switch f.typ(item) {
		case "dotted_name":
			if item == mod {
				continue
			}
			name := f.text(item)
			f.bindImport(name, module+"."+name)
		case "aliased_import":
			name := f.text(f.field(item, "name"))
			alias := f.text(f.field(item, "alias"))
			f.bindImport(alias, module+"."+name)
		case "wildcard_import":
			f.b.Diag(model.Diagnostic{Code: model.DiagUnresolvedBinding, Severity: model.SeverityInfo, Message: "wildcard import from " + module + "; names it introduces are not tracked"})
		}
	}
}

func (f *pyFrontend) bindImport(name, qualified string) {
	root := strings.SplitN(qualified, ".", 2)[0]
	if !pyLibraries[root] {
		f.scope.set(name, &binding{kind: bindOpaque, qualified: qualified})
		return
	}
	f.scope.set(name, &binding{kind: bindImport, qualified: qualified})
}

// assign records simple assignments; anything else makes the target opaque
// (and shadows library meaning when the name had one).
func (f *pyFrontend) assign(parent string, n *ts.Node, fl flow) {
	left, right := f.field(n, "left"), f.field(n, "right")
	if right == nil {
		return
	}
	val, isPath := f.evalPath(parent, right, fl)
	if left == nil {
		return
	}
	targets := []*ts.Node{left}
	if f.typ(left) == "pattern_list" || f.typ(left) == "tuple_pattern" {
		targets = f.named(left)
		val, isPath = symbolic("<unpacked>", "tuple unpacking is not modeled"), false
	}
	for _, t := range targets {
		if f.typ(t) != "identifier" {
			continue // attribute/subscript targets do not rebind names
		}
		name := f.text(t)
		prev := f.scope.lookup(name)
		shadow := prev != nil && prev.kind == bindImport
		if f.typ(n) == "augmented_assignment" {
			val = symbolic(name, "augmented assignment is not modeled")
		}
		f.scope.set(name, &binding{kind: bindValue, value: val, isPath: isPath, shadowed: shadow})
		if shadow {
			f.b.Diag(model.Diagnostic{Code: model.DiagShadowed, Severity: model.SeverityInfo, Message: "python name " + name + " rebound; it no longer denotes " + prev.qualified})
		}
	}
}

// eval evaluates an expression for its side effects and value.
func (f *pyFrontend) eval(parent string, n *ts.Node, fl flow) model.Value {
	v, _ := f.evalPath(parent, n, fl)
	return v
}

// evalPath is eval plus whether the value is a pathlib.Path object.
func (f *pyFrontend) evalPath(parent string, n *ts.Node, fl flow) (model.Value, bool) {
	if n == nil {
		return model.Unknown(), false
	}
	switch f.typ(n) {
	case "string":
		return f.str(parent, n, fl), false
	case "concatenated_string":
		out := model.Literal("")
		for _, s := range f.named(n) {
			out = concat(out, f.str(parent, s, fl))
		}
		return out, false
	case "integer", "float", "true", "false", "none":
		return model.Literal(f.text(n)), false
	case "identifier":
		name := f.text(n)
		b := f.scope.lookup(name)
		switch {
		case b == nil:
			return symbolic(name, name+" is not assigned in this program"), false
		case b.kind == bindValue:
			v := b.value
			if v.Knowledge == model.KnowledgeLiteral {
				v.Knowledge = model.KnowledgeDerived
			}
			return v, b.isPath
		case b.kind == bindImport:
			return symbolic(name, "module or function object"), false
		}
		return symbolic(name, "value of "+name+" is not modeled"), false
	case "parenthesized_expression", "await":
		if c := n.NamedChild(0); c != nil {
			return f.evalPath(parent, c, fl)
		}
	case "binary_operator":
		left, lp := f.evalPath(parent, f.field(n, "left"), fl)
		right, _ := f.evalPath(parent, f.field(n, "right"), fl)
		switch f.text(f.field(n, "operator")) {
		case "+":
			return concat(left, right), false
		case "/":
			if lp {
				return joinPath(left, right), true
			}
			return symbolic(f.text(n), "division of non-path values"), false
		case "%":
			return symbolic(f.text(n), "% formatting is not evaluated"), false
		}
		return symbolic(f.text(n), "operator not modeled"), false
	case "call":
		return f.callExpr(parent, n, fl)
	case "attribute":
		obj, isPath := f.evalPath(parent, f.field(n, "object"), fl)
		attr := f.text(f.field(n, "attribute"))
		if isPath {
			switch attr {
			case "parent":
				if obj.Concrete() {
					return model.Value{Knowledge: model.KnowledgeDerived, Text: parentDir(obj.Text)}, true
				}
			case "name", "stem", "suffix":
				return symbolic(f.text(n), "path component"), false
			}
		}
		return symbolic(f.text(n), "attribute value is not modeled"), false
	case "subscript", "conditional_expression", "list_comprehension", "generator_expression", "dictionary", "set", "dictionary_comprehension", "lambda", "not_operator", "boolean_operator", "comparison_operator", "unary_operator", "ellipsis", "slice", "keyword_argument", "list_splat", "dictionary_splat", "yield":
		for _, c := range f.named(n) {
			if f.typ(c) == "call" {
				f.callExpr(parent, c, fl.with(model.ReachConditional))
			}
		}
		return symbolic(f.text(n), f.typ(n)+" is not evaluated"), false
	case "list", "tuple":
		for _, c := range f.named(n) {
			f.evalPath(parent, c, fl)
		}
		return symbolic(f.text(n), "sequence value"), false
	case "ERROR":
		f.unknown(parent, n, fl, "python syntax error region")
		return model.Unknown(), false
	}
	return symbolic(f.text(n), "expression "+f.typ(n)+" is not modeled"), false
}

func parentDir(p string) string {
	if i := strings.LastIndex(p, "/"); i > 0 {
		return p[:i]
	}
	if strings.HasPrefix(p, "/") {
		return "/"
	}
	return "."
}

// str evaluates a string literal; f-strings with interpolation are symbolic.
func (f *pyFrontend) str(parent string, n *ts.Node, fl flow) model.Value {
	var b strings.Builder
	sym := false
	prefix := ""
	for _, c := range f.named(n) {
		switch f.typ(c) {
		case "string_start":
			prefix = strings.ToLower(strings.TrimRight(f.text(c), `"'`))
		case "string_content":
			// The grammar keeps backslash escapes inside string_content.
			if strings.Contains(prefix, "r") {
				b.WriteString(f.text(c))
			} else {
				b.WriteString(unescapePy(f.text(c)))
			}
		case "escape_sequence":
			b.WriteString(unescapePy(f.text(c)))
		case "interpolation":
			sym = true
			if e := f.field(c, "expression"); e != nil {
				v := f.eval(parent, e, fl)
				if v.Concrete() {
					b.WriteString(v.Text)
					sym = false
					continue
				}
				b.WriteString("{" + f.text(e) + "}")
			}
		}
	}
	if strings.Contains(prefix, "b") {
		return model.Literal(b.String())
	}
	if sym {
		return symbolic(b.String(), "f-string interpolates non-literal values")
	}
	return model.Literal(b.String())
}

func unescapePy(s string) string {
	r := strings.NewReplacer(`\n`, "\n", `\t`, "\t", `\\`, `\`, `\'`, "'", `\"`, `"`, `\r`, "\r")
	return r.Replace(s)
}

// callArgs evaluates positional and keyword arguments.
func (f *pyFrontend) callArgs(parent string, n *ts.Node, fl flow) (pos []model.Value, kw map[string]model.Value, raw []*ts.Node) {
	kw = map[string]model.Value{}
	args := f.field(n, "arguments")
	if args == nil {
		return
	}
	for _, a := range f.named(args) {
		if f.typ(a) == "keyword_argument" {
			kw[f.text(f.field(a, "name"))] = f.eval(parent, f.field(a, "value"), fl)
			continue
		}
		if f.typ(a) == "comment" {
			continue
		}
		raw = append(raw, a)
		v, isPath := f.evalPath(parent, a, fl)
		_ = isPath
		pos = append(pos, v)
	}
	return
}

// callee resolves what a call targets: a qualified library name, a method on
// a Path value, a user function, or nothing known.
func (f *pyFrontend) callee(parent string, fn *ts.Node, fl flow) (qualified string, recv *model.Value, user *binding) {
	switch f.typ(fn) {
	case "identifier":
		b := f.scope.lookup(f.text(fn))
		if b == nil {
			return "", nil, nil
		}
		switch b.kind {
		case bindImport:
			return b.qualified, nil, nil
		case bindFunc:
			return "", nil, b
		}
		return "", nil, nil
	case "attribute":
		obj := f.field(fn, "object")
		attr := f.text(f.field(fn, "attribute"))
		if f.typ(obj) == "identifier" || f.typ(obj) == "attribute" {
			if q, _, _ := f.moduleRef(obj); q != "" {
				return q + "." + attr, nil, nil
			}
		}
		v, isPath := f.evalPath(parent, obj, fl)
		if isPath {
			return "pathlib.Path." + attr, &v, nil
		}
		if b := f.scope.lookup(f.text(obj)); b != nil && b.kind == bindValue && f.typ(obj) == "identifier" {
			return "<value>." + attr, &v, nil
		}
		return "", nil, nil
	}
	return "", nil, nil
}

// moduleRef resolves os / os.path style references to a qualified module.
func (f *pyFrontend) moduleRef(n *ts.Node) (string, bool, bool) {
	switch f.typ(n) {
	case "identifier":
		b := f.scope.lookup(f.text(n))
		if b != nil && b.kind == bindImport {
			return b.qualified, true, false
		}
	case "attribute":
		if q, ok, _ := f.moduleRef(f.field(n, "object")); ok {
			return q + "." + f.text(f.field(n, "attribute")), true, false
		}
	}
	return "", false, false
}

func (f *pyFrontend) callExpr(parent string, n *ts.Node, fl flow) (model.Value, bool) {
	fn := f.field(n, "function")
	if fn == nil {
		return model.Unknown(), false
	}
	qualified, recv, user := f.callee(parent, fn, fl)
	pos, kw, raw := f.callArgs(parent, n, fl)
	assume := qualified + " denotes the standard library API"
	arg := func(i int) model.Value {
		if i < len(pos) {
			return pos[i]
		}
		return model.Value{}
	}
	if user != nil {
		return f.userCall(parent, n, fn, user, pos, fl), false
	}
	switch qualified {
	case "":
		// Unknown callee: its effects are unknown; note only if it looks like
		// a shadowed library name.
		if f.typ(fn) == "identifier" {
			if b := f.scope.lookup(f.text(fn)); b != nil && b.shadowed {
				f.b.Diag(model.Diagnostic{Code: model.DiagShadowed, Severity: model.SeverityInfo, Message: "call to rebound name " + f.text(fn) + " is not the library function"})
			}
		}
		return symbolic(f.text(n), "call result is not modeled"), false
	case "pathlib.Path", "pathlib.PurePath", "pathlib.PosixPath":
		if len(pos) == 0 {
			return model.Value{Knowledge: model.KnowledgeDerived, Text: "."}, true
		}
		v := pos[0]
		for _, more := range pos[1:] {
			v = joinPath(v, more)
		}
		if v.Knowledge == model.KnowledgeLiteral {
			v.Knowledge = model.KnowledgeDerived
		}
		return v, true
	case "os.path.join":
		if len(pos) == 0 {
			return model.Unknown(), false
		}
		v := pos[0]
		for _, more := range pos[1:] {
			v = joinPath(v, more)
		}
		return v, false
	case "os.path.abspath", "os.path.realpath", "os.path.normpath", "os.path.expanduser", "str", "os.fspath", "os.path.expandvars":
		v := arg(0)
		if (qualified == "os.path.expanduser" && strings.HasPrefix(v.Text, "~")) || qualified == "os.path.expandvars" {
			return symbolic(v.Text, qualified+" depends on the environment"), false
		}
		if qualified == "os.path.realpath" {
			v.Assumptions = append(v.Assumptions, "realpath resolves symlinks at runtime; lexical value shown")
		}
		return v, false
	case "os.path.dirname":
		if v := arg(0); v.Concrete() {
			return model.Value{Knowledge: model.KnowledgeDerived, Text: parentDir(v.Text)}, false
		}
		return symbolic(f.text(n), "dirname of non-literal"), false
	case "os.getcwd", "pathlib.Path.cwd":
		return f.cwd, qualified == "pathlib.Path.cwd"
	case "os.chdir":
		id := f.call(parent, n, qualified, pos, fl, assume)
		target := arg(0)
		switch {
		case target.Concrete() && strings.HasPrefix(target.Text, "/"):
			f.cwd = model.Value{Knowledge: model.KnowledgeDerived, Text: target.Text, Assumptions: []string{"os.chdir succeeded"}}
		case target.Concrete() && f.cwd.Concrete():
			f.cwd = joinPath(f.cwd, target)
			f.cwd.Assumptions = []string{"os.chdir succeeded"}
		default:
			f.cwd = model.Value{Knowledge: model.KnowledgeSymbolic, Text: target.Text, Assumptions: []string{"os.chdir to a non-literal directory"}}
		}
		f.b.Node(id).Context = f.nodeContext()
		return model.Unknown(), false
	case "builtins.open", "io.open", "pathlib.Path.open":
		p := arg(0)
		mode := arg(1)
		if recv != nil {
			p, mode = *recv, arg(0)
		}
		if m, ok := kw["mode"]; ok {
			mode = m
		}
		f.call(parent, n, qualified, pos, fl, assume, f.fileActions(p, mode, "r")...)
		return symbolic(f.text(n), "file object"), false
	case "pathlib.Path.read_text", "pathlib.Path.read_bytes":
		f.call(parent, n, qualified, pos, fl, assume, model.Action{Kind: model.FSRead, Resources: []model.Resource{f.resource(*recv, "target")}})
		return symbolic(f.text(n), "file contents"), false
	case "pathlib.Path.write_text", "pathlib.Path.write_bytes":
		f.call(parent, n, qualified, pos, fl, assume, model.Action{Kind: model.FSWrite, Write: &model.WriteDetail{Mode: model.WriteTruncate}, Resources: []model.Resource{f.resource(*recv, "target")}})
	case "pathlib.Path.unlink", "os.remove", "os.unlink":
		p := arg(0)
		if recv != nil {
			p = *recv
		}
		f.call(parent, n, qualified, pos, fl, assume, model.Action{Kind: model.FSDelete, Delete: &model.DeleteDetail{}, Resources: []model.Resource{f.resource(p, "target")}})
	case "pathlib.Path.rmdir", "os.rmdir", "os.removedirs":
		p := arg(0)
		if recv != nil {
			p = *recv
		}
		f.call(parent, n, qualified, pos, fl, assume, model.Action{Kind: model.FSDelete, Delete: &model.DeleteDetail{}, Resources: []model.Resource{f.resource(p, "target")}})
	case "shutil.rmtree":
		f.call(parent, n, qualified, pos, fl, assume, model.Action{Kind: model.FSDelete, Delete: &model.DeleteDetail{Recursive: true}, Resources: []model.Resource{f.resource(arg(0), "target")}})
	case "pathlib.Path.mkdir", "os.mkdir", "os.makedirs":
		p := arg(0)
		if recv != nil {
			p = *recv
		}
		mode := model.WriteCreateExclusive
		if ok, has := kw["exist_ok"]; has && ok.Text == "True" || qualified == "os.makedirs" {
			mode = model.WriteUnknown
		}
		f.call(parent, n, qualified, pos, fl, assume, model.Action{Kind: model.FSWrite, Write: &model.WriteDetail{Mode: mode, Directory: true}, Resources: []model.Resource{f.resource(p, "target")}})
	case "pathlib.Path.touch":
		f.call(parent, n, qualified, pos, fl, assume, model.Action{Kind: model.FSWrite, Write: &model.WriteDetail{Mode: model.WriteUnknown}, Resources: []model.Resource{f.resource(*recv, "target")}})
	case "pathlib.Path.rename", "pathlib.Path.replace", "os.rename", "os.replace", "shutil.move":
		src, dst := arg(0), arg(1)
		if recv != nil {
			src, dst = *recv, arg(0)
		}
		f.call(parent, n, qualified, pos, fl, assume, model.Action{Kind: model.FSMove, Resources: []model.Resource{f.resource(src, "source"), f.resource(dst, "destination")}})
	case "shutil.copy", "shutil.copy2", "shutil.copyfile", "shutil.copytree":
		f.call(parent, n, qualified, pos, fl, assume, model.Action{Kind: model.FSCopy, Resources: []model.Resource{f.resource(arg(0), "source"), f.resource(arg(1), "destination")}})
	case "pathlib.Path.exists", "pathlib.Path.is_file", "pathlib.Path.is_dir", "pathlib.Path.stat", "os.path.exists", "os.path.isfile", "os.path.isdir", "os.path.getsize", "os.stat", "os.path.getmtime", "os.access", "os.chmod", "os.path.islink", "os.path.lexists":
		p := arg(0)
		if recv != nil {
			p = *recv
		}
		f.call(parent, n, qualified, pos, fl, assume, model.Action{Kind: model.FSMetadata, Resources: []model.Resource{f.resource(p, "target")}})
		return symbolic(f.text(n), "metadata result"), false
	case "pathlib.Path.iterdir", "os.listdir", "os.scandir", "os.walk":
		p := arg(0)
		if recv != nil {
			p = *recv
		}
		if p.Knowledge == "" {
			p = model.Literal(".")
		}
		f.call(parent, n, qualified, pos, fl, assume, model.Action{Kind: model.FSList, Resources: []model.Resource{f.resource(p, "scope")}})
		return symbolic("<entry of "+p.Text+">", "one directory entry"), recv != nil
	case "pathlib.Path.glob", "pathlib.Path.rglob", "glob.glob", "glob.iglob":
		scope, pattern := model.Literal("."), arg(0)
		if recv != nil {
			scope = *recv
		} else {
			scope = pattern
		}
		r := f.resource(scope, "scope")
		if recv == nil {
			r.Knowledge = model.KnowledgeGlob
		}
		a := model.Action{Kind: model.SearchPath, Resources: []model.Resource{r}, Search: &model.SearchDetail{Pattern: pattern}}
		f.call(parent, n, qualified, pos, fl, assume, a)
		// The elements are paths (pathlib) or path strings (glob module).
		return symbolic("<match of "+pattern.Text+">", "one match of the pattern"), recv != nil
	case "subprocess.run", "subprocess.call", "subprocess.check_call", "subprocess.check_output", "subprocess.Popen":
		f.subprocess(parent, n, qualified, pos, kw, raw, fl)
		return symbolic(f.text(n), "process result"), false
	case "subprocess.getoutput", "subprocess.getstatusoutput", "os.system", "os.popen":
		f.spawn(parent, n, fl, qualified, arg(0), nil, true, nil)
		return symbolic(f.text(n), "process output"), false
	case "os.execv", "os.execvp", "os.execl", "os.execlp", "os.execve", "os.execvpe":
		f.spawn(parent, n, fl, qualified, arg(0), nil, false, nil)
	case "<value>.write", "<value>.read", "<value>.close", "<value>.readlines", "<value>.writelines", "<value>.flush":
		// Operations on a file object already accounted for by open().
	case "sys.exit":
	default:
		if strings.HasPrefix(qualified, "<value>.") {
			return symbolic(f.text(n), "method on a value is not modeled"), false
		}
		id := f.call(parent, n, qualified, pos, fl, assume)
		f.b.Node(id).Detail = "library call without modeled effects"
	}
	return symbolic(f.text(n), "call result"), false
}

// subprocess handles the subprocess module's argument conventions.
func (f *pyFrontend) subprocess(parent string, n *ts.Node, qualified string, pos []model.Value, kw map[string]model.Value, raw []*ts.Node, fl flow) {
	shell := false
	if s, ok := kw["shell"]; ok {
		if s.Text == "True" {
			shell = true
		} else if !s.Concrete() {
			id := f.call(parent, n, qualified, pos, fl, qualified+" denotes the standard library API")
			f.b.AddAction(id, model.Action{Kind: model.ProcessUnresolved, Evidence: model.Evidence{Basis: model.BasisBoundedValue, Spans: []model.SourceSpan{f.span(n)}}, Process: &model.ProcessDetail{Executable: model.Unknown(), Reason: "shell= is not literal"}})
			return
		}
	}
	var cwd *model.Value
	if c, ok := kw["cwd"]; ok {
		cwd = &c
	}
	argsNode := kw["args"]
	var first *ts.Node
	if len(raw) > 0 {
		first = raw[0]
	}
	switch {
	case first != nil && (f.typ(first) == "list" || f.typ(first) == "tuple"):
		var argv []model.Value
		for _, e := range f.named(first) {
			argv = append(argv, f.eval(parent, e, fl))
		}
		if len(argv) == 0 {
			return
		}
		f.spawn(parent, n, fl, qualified, argv[0], argv[1:], shell, cwd)
	case first != nil:
		f.spawn(parent, n, fl, qualified, pos[0], nil, shell, cwd)
	case argsNode.Knowledge != "":
		f.spawn(parent, n, fl, qualified, argsNode, nil, shell, cwd)
	default:
		id := f.call(parent, n, qualified, pos, fl, qualified+" denotes the standard library API")
		f.b.AddAction(id, model.Action{Kind: model.ProcessUnresolved, Evidence: model.Evidence{Basis: model.BasisBoundedValue, Spans: []model.SourceSpan{f.span(n)}}, Process: &model.ProcessDetail{Executable: model.Unknown(), Reason: "subprocess arguments are not literal"}})
	}
}

// userCall expands a function defined in this program at its call site,
// binding positional parameters to the argument values, once per depth.
func (f *pyFrontend) userCall(parent string, n, fn *ts.Node, b *binding, args []model.Value, fl flow) model.Value {
	name := f.text(fn)
	sp := f.span(n)
	id := f.b.AddNode(model.Node{Parent: parent, Kind: model.NodeCommand, Span: &sp, Guard: fl.guard, Reach: fl.reach, Syntax: model.SyntaxParsed, Detail: "function call",
		Command: &model.Command{Executable: model.Value{Knowledge: model.KnowledgeDerived, Text: name}, Args: args, Rule: "function", Assumptions: []string{"invokes the function defined in this program"}}, Context: f.nodeContext()})
	if f.calling[name] >= 1 {
		f.b.Diag(model.Diagnostic{Code: model.DiagLimitReached, Severity: model.SeverityInfo, Node: id, Message: "recursive call to " + name + " not expanded"})
		return symbolic(f.text(n), "recursive call")
	}
	f.calling[name]++
	defer func() { f.calling[name]-- }()
	outer := f.scope
	f.scope = newScope(outer)
	defer func() { f.scope = outer }()
	if params := f.field(b.fn, "parameters"); params != nil {
		i := 0
		for _, p := range f.named(params) {
			var pname string
			switch f.typ(p) {
			case "identifier":
				pname = f.text(p)
			case "default_parameter", "typed_parameter", "typed_default_parameter":
				if nm := f.field(p, "name"); nm != nil {
					pname = f.text(nm)
				} else if c := p.NamedChild(0); c != nil && f.typ(c) == "identifier" {
					pname = f.text(c)
				}
			default:
				continue
			}
			if pname == "" {
				continue
			}
			if i < len(args) {
				f.scope.set(pname, &binding{kind: bindValue, value: args[i]})
			} else {
				f.scope.set(pname, &binding{kind: bindValue, value: symbolic(pname, "parameter without a literal argument")})
			}
			i++
		}
	}
	if body := f.field(b.fn, "body"); body != nil {
		f.block(id, body, fl.with(fl.reach))
	}
	return symbolic(f.text(n), "return value of "+name)
}
