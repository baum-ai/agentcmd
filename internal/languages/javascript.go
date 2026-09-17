package languages

import (
	"strings"

	ts "github.com/odvcencio/gotreesitter"

	"github.com/lastpersonlabs/toolparser/model"
)

// jsFrontend implements the JavaScript subset: require/import bindings,
// simple declarations, string and path construction, qualified fs /
// fs.promises / child_process calls, and control-flow qualification.
// Callbacks passed to known calls are analyzed as conditional bodies; other
// function bodies are latent until called at top level.
type jsFrontend struct{ *frontend }

// jsModules maps import sources to canonical module names.
var jsModules = map[string]string{
	"fs": "fs", "node:fs": "fs", "fs/promises": "fs.promises", "node:fs/promises": "fs.promises",
	"child_process": "child_process", "node:child_process": "child_process",
	"path": "path", "node:path": "path", "os": "os", "node:os": "os", "process": "process", "node:process": "process",
	"fs-extra": "fs", "graceful-fs": "fs",
}

func (f *jsFrontend) program(parent string, root *ts.Node, fl flow) {
	f.scope.set("process", &binding{kind: bindImport, qualified: "process"})
	f.scope.set("require", &binding{kind: bindImport, qualified: "require"})
	f.scope.set("__dirname", &binding{kind: bindValue, value: symbolic("__dirname", "directory of the executing script")})
	f.scope.set("__filename", &binding{kind: bindValue, value: symbolic("__filename", "path of the executing script")})
	f.block(parent, root, fl)
}

func (f *jsFrontend) block(parent string, n *ts.Node, fl flow) {
	for _, st := range f.named(n) {
		if !f.step(parent, st, fl) {
			return
		}
		f.stmt(parent, st, fl)
	}
}

func (f *jsFrontend) stmt(parent string, n *ts.Node, fl flow) {
	switch f.typ(n) {
	case "comment", "empty_statement", "break_statement", "continue_statement", "debugger_statement", "hash_bang_line":
	case "expression_statement":
		for _, e := range f.named(n) {
			f.eval(parent, e, fl)
		}
	case "lexical_declaration", "variable_declaration":
		for _, d := range f.named(n) {
			if f.typ(d) == "variable_declarator" {
				f.declare(parent, f.field(d, "name"), f.field(d, "value"), fl)
			}
		}
	case "import_statement":
		f.importStmt(n)
	case "export_statement":
		for _, c := range f.named(n) {
			if strings.HasSuffix(f.typ(c), "declaration") || f.typ(c) == "expression_statement" {
				f.stmt(parent, c, fl)
			}
		}
	case "statement_block":
		f.block(parent, n, fl)
	case "if_statement":
		id := f.container(parent, n, model.NodeIf, "if", fl)
		f.eval(id, f.field(n, "condition"), fl.with(fl.reach))
		if c := f.field(n, "consequence"); c != nil {
			f.stmt(id, c, fl.with(model.ReachConditional))
		}
		if alt := f.field(n, "alternative"); alt != nil {
			for _, c := range f.named(alt) {
				f.stmt(id, c, fl.with(model.ReachConditional))
			}
		}
	case "switch_statement":
		id := f.container(parent, n, model.NodeIf, "switch", fl)
		if body := f.field(n, "body"); body != nil {
			for _, c := range f.named(body) {
				for _, s := range f.named(c) {
					if strings.HasSuffix(f.typ(s), "statement") || strings.HasSuffix(f.typ(s), "declaration") || f.typ(s) == "statement_block" {
						f.stmt(id, s, fl.with(model.ReachConditional))
					}
				}
			}
		}
	case "for_statement", "for_in_statement", "while_statement", "do_statement":
		id := f.container(parent, n, model.NodeLoop, strings.TrimSuffix(f.typ(n), "_statement"), fl)
		if f.typ(n) == "for_in_statement" {
			if left := f.field(n, "left"); left != nil && f.typ(left) == "identifier" {
				f.scope.set(f.text(left), &binding{kind: bindValue, value: symbolic(f.text(left), "loop variable over "+f.text(f.field(n, "right")))})
			}
			f.eval(id, f.field(n, "right"), fl.with(fl.reach))
		}
		if body := f.field(n, "body"); body != nil {
			f.stmt(id, body, fl.with(model.ReachRepeated))
		}
	case "try_statement":
		id := f.container(parent, n, model.NodeGroup, "try", fl)
		if body := f.field(n, "body"); body != nil {
			f.block(id, body, fl.with(fl.reach))
		}
		if h := f.field(n, "handler"); h != nil {
			if body := f.field(h, "body"); body != nil {
				f.block(id, body, fl.with(model.ReachConditional))
			}
		}
		if fin := f.field(n, "finalizer"); fin != nil {
			if body := f.field(fin, "body"); body != nil {
				f.block(id, body, fl.with(fl.reach))
			}
		}
	case "function_declaration", "generator_function_declaration":
		name := f.field(n, "name")
		f.container(parent, n, model.NodeFunction, f.text(name), fl)
		f.scope.set(f.text(name), &binding{kind: bindFunc, fn: n})
	case "class_declaration":
		name := f.field(n, "name")
		f.container(parent, n, model.NodeFunction, "class "+f.text(name), fl)
		f.scope.set(f.text(name), &binding{kind: bindOpaque})
	case "return_statement", "throw_statement":
		for _, e := range f.named(n) {
			f.eval(parent, e, fl)
		}
	case "ERROR":
		f.unknown(parent, n, fl, "javascript syntax error region")
	default:
		if strings.HasSuffix(f.typ(n), "_statement") || strings.HasSuffix(f.typ(n), "_declaration") {
			f.unknown(parent, n, fl, "javascript "+f.typ(n)+" is not modeled")
			return
		}
		f.eval(parent, n, fl)
	}
}

// declare binds a declarator target (identifier or destructuring pattern).
func (f *jsFrontend) declare(parent string, target, value *ts.Node, fl flow) {
	if target == nil {
		return
	}
	var val model.Value
	module := ""
	if value != nil {
		val, module = f.evalModule(parent, value, fl)
	}
	switch f.typ(target) {
	case "identifier":
		name := f.text(target)
		if module != "" {
			f.scope.set(name, &binding{kind: bindImport, qualified: module})
			return
		}
		if value == nil {
			f.scope.set(name, &binding{kind: bindValue, value: symbolic(name, "declared without a value")})
			return
		}
		f.bindValue(name, val)
	case "object_pattern":
		for _, p := range f.named(target) {
			switch f.typ(p) {
			case "shorthand_property_identifier_pattern":
				name := f.text(p)
				if module != "" {
					f.scope.set(name, &binding{kind: bindImport, qualified: module + "." + name})
				} else {
					f.scope.set(name, &binding{kind: bindValue, value: symbolic(name, "destructured property")})
				}
			case "pair_pattern":
				key, v := f.field(p, "key"), f.field(p, "value")
				if v != nil && f.typ(v) == "identifier" {
					if module != "" {
						f.scope.set(f.text(v), &binding{kind: bindImport, qualified: module + "." + f.text(key)})
					} else {
						f.scope.set(f.text(v), &binding{kind: bindValue, value: symbolic(f.text(v), "destructured property")})
					}
				}
			}
		}
	case "array_pattern":
		for _, p := range f.named(target) {
			if f.typ(p) == "identifier" {
				f.scope.set(f.text(p), &binding{kind: bindValue, value: symbolic(f.text(p), "destructured element")})
			}
		}
	}
}

func (f *jsFrontend) bindValue(name string, val model.Value) {
	prev := f.scope.lookup(name)
	shadow := prev != nil && prev.kind == bindImport
	f.scope.set(name, &binding{kind: bindValue, value: val, shadowed: shadow})
	if shadow {
		f.b.Diag(model.Diagnostic{Code: model.DiagShadowed, Severity: model.SeverityInfo, Message: "javascript name " + name + " rebound; it no longer denotes " + prev.qualified})
	}
}

func (f *jsFrontend) importStmt(n *ts.Node) {
	src := f.field(n, "source")
	if src == nil {
		return
	}
	module := jsModules[f.strText(src)]
	for _, clause := range f.named(n) {
		if f.typ(clause) != "import_clause" {
			continue
		}
		for _, c := range f.named(clause) {
			switch f.typ(c) {
			case "identifier":
				f.bindModule(f.text(c), module, "")
			case "namespace_import":
				if id := c.NamedChild(0); id != nil {
					f.bindModule(f.text(id), module, "")
				}
			case "named_imports":
				for _, spec := range f.named(c) {
					name := f.field(spec, "name")
					alias := f.field(spec, "alias")
					local := name
					if alias != nil {
						local = alias
					}
					if name != nil {
						f.bindModule(f.text(local), module, f.text(name))
					}
				}
			}
		}
	}
}

func (f *jsFrontend) bindModule(local, module, member string) {
	if module == "" {
		f.scope.set(local, &binding{kind: bindOpaque})
		return
	}
	q := module
	if member != "" && member != "default" {
		q += "." + member
	}
	f.scope.set(local, &binding{kind: bindImport, qualified: q})
}

// strText returns the content of a string literal node.
func (f *jsFrontend) strText(n *ts.Node) string {
	var b strings.Builder
	for _, c := range f.named(n) {
		switch f.typ(c) {
		case "string_fragment":
			b.WriteString(f.text(c))
		case "escape_sequence":
			b.WriteString(unescapePy(f.text(c)))
		}
	}
	return b.String()
}

// evalModule is eval that also reports a module name when the expression is
// require('mod') or a module member reference.
func (f *jsFrontend) evalModule(parent string, n *ts.Node, fl flow) (model.Value, string) {
	if f.typ(n) == "call_expression" {
		fn := f.field(n, "function")
		if fn != nil && f.typ(fn) == "identifier" && f.text(fn) == "require" {
			args := f.field(n, "arguments")
			if args != nil && args.NamedChildCount() == 1 && f.typ(args.NamedChild(0)) == "string" {
				mod := jsModules[f.strText(args.NamedChild(0))]
				if mod == "" {
					return symbolic(f.text(n), "module not modeled"), ""
				}
				return model.Value{}, mod
			}
			return symbolic(f.text(n), "dynamic require"), ""
		}
	}
	if q, ok := f.moduleRef(n); ok {
		return model.Value{}, q
	}
	if f.typ(n) == "await_expression" {
		if c := n.NamedChild(0); c != nil && f.typ(c) == "call_expression" {
			fn := f.field(c, "function")
			if fn != nil && f.typ(fn) == "import" {
				args := f.field(c, "arguments")
				if args != nil && args.NamedChildCount() == 1 && f.typ(args.NamedChild(0)) == "string" {
					if mod := jsModules[f.strText(args.NamedChild(0))]; mod != "" {
						return model.Value{}, mod
					}
				}
			}
		}
	}
	return f.eval(parent, n, fl), ""
}

// moduleRef resolves fs / fs.promises / require('fs') style references.
func (f *jsFrontend) moduleRef(n *ts.Node) (string, bool) {
	switch f.typ(n) {
	case "call_expression":
		fn := f.field(n, "function")
		if fn != nil && f.typ(fn) == "identifier" && f.text(fn) == "require" {
			if b := f.scope.lookup("require"); b == nil || b.qualified != "require" {
				return "", false
			}
			args := f.field(n, "arguments")
			if args != nil && args.NamedChildCount() == 1 && f.typ(args.NamedChild(0)) == "string" {
				if mod := jsModules[f.strText(args.NamedChild(0))]; mod != "" {
					return mod, true
				}
			}
		}
	case "parenthesized_expression":
		if c := n.NamedChild(0); c != nil {
			return f.moduleRef(c)
		}
	case "identifier":
		if b := f.scope.lookup(f.text(n)); b != nil && b.kind == bindImport {
			return b.qualified, true
		}
	case "member_expression":
		if q, ok := f.moduleRef(f.field(n, "object")); ok {
			prop := f.field(n, "property")
			if prop == nil {
				return "", false
			}
			return q + "." + f.text(prop), true
		}
	}
	return "", false
}

func (f *jsFrontend) eval(parent string, n *ts.Node, fl flow) model.Value {
	if n == nil {
		return model.Unknown()
	}
	switch f.typ(n) {
	case "string":
		return model.Literal(f.strText(n))
	case "template_string":
		var b strings.Builder
		sym := false
		for _, c := range f.named(n) {
			switch f.typ(c) {
			case "string_fragment":
				b.WriteString(f.text(c))
			case "escape_sequence":
				b.WriteString(unescapePy(f.text(c)))
			case "template_substitution":
				v := f.eval(parent, c.NamedChild(0), fl)
				if v.Concrete() {
					b.WriteString(v.Text)
				} else {
					sym = true
					b.WriteString("${" + f.text(c.NamedChild(0)) + "}")
				}
			}
		}
		if sym {
			return symbolic(b.String(), "template literal interpolates non-literal values")
		}
		return model.Literal(b.String())
	case "number", "true", "false", "null", "undefined":
		return model.Literal(f.text(n))
	case "identifier":
		name := f.text(n)
		b := f.scope.lookup(name)
		switch {
		case b == nil:
			return symbolic(name, name+" is not assigned in this program")
		case b.kind == bindValue:
			v := b.value
			if v.Knowledge == model.KnowledgeLiteral {
				v.Knowledge = model.KnowledgeDerived
			}
			return v
		}
		return symbolic(name, "value of "+name+" is not modeled")
	case "parenthesized_expression", "await_expression", "spread_element":
		if c := n.NamedChild(0); c != nil {
			return f.eval(parent, c, fl)
		}
	case "binary_expression":
		left := f.eval(parent, f.field(n, "left"), fl)
		right := f.eval(parent, f.field(n, "right"), fl)
		if f.text(f.field(n, "operator")) == "+" {
			return concat(left, right)
		}
		return symbolic(f.text(n), "operator not modeled")
	case "assignment_expression":
		val := f.eval(parent, f.field(n, "right"), fl)
		if left := f.field(n, "left"); left != nil && f.typ(left) == "identifier" {
			f.bindValue(f.text(left), val)
		}
		return val
	case "augmented_assignment_expression":
		f.eval(parent, f.field(n, "right"), fl)
		if left := f.field(n, "left"); left != nil && f.typ(left) == "identifier" {
			f.bindValue(f.text(left), symbolic(f.text(left), "augmented assignment is not modeled"))
		}
		return model.Unknown()
	case "call_expression":
		return f.callExpr(parent, n, fl)
	case "new_expression":
		return f.newExpr(parent, n, fl)
	case "member_expression":
		if q, ok := f.moduleRef(n); ok {
			switch q {
			case "process.env":
				return symbolic(f.text(n), "environment")
			case "process.argv":
				return symbolic(f.text(n), "command-line arguments")
			}
			return symbolic(f.text(n), "module member")
		}
		f.eval(parent, f.field(n, "object"), fl)
		return symbolic(f.text(n), "property value is not modeled")
	case "arrow_function", "function_expression", "function", "class", "generator_function":
		return symbolic("<function>", "function value")
	case "array":
		for _, c := range f.named(n) {
			f.eval(parent, c, fl)
		}
		return symbolic(f.text(n), "array value")
	case "object":
		for _, p := range f.named(n) {
			if f.typ(p) == "pair" {
				f.eval(parent, f.field(p, "value"), fl)
			}
		}
		return symbolic(f.text(n), "object value")
	case "ternary_expression", "unary_expression", "update_expression", "subscript_expression", "sequence_expression", "yield_expression":
		for _, c := range f.named(n) {
			f.eval(parent, c, fl.with(model.ReachConditional))
		}
		return symbolic(f.text(n), f.typ(n)+" is not evaluated")
	case "ERROR":
		f.unknown(parent, n, fl, "javascript syntax error region")
		return model.Unknown()
	}
	return symbolic(f.text(n), "expression "+f.typ(n)+" is not modeled")
}

func (f *jsFrontend) newExpr(parent string, n *ts.Node, fl flow) model.Value {
	ctor := f.field(n, "constructor")
	if ctor != nil && f.typ(ctor) == "identifier" && f.text(ctor) == "Promise" {
		// new Promise(executor): the executor runs synchronously.
		if args := f.field(n, "arguments"); args != nil {
			for _, a := range f.named(args) {
				f.callback(parent, a, fl, "Promise executor runs synchronously")
			}
		}
		return symbolic(f.text(n), "promise")
	}
	if args := f.field(n, "arguments"); args != nil {
		for _, a := range f.named(args) {
			f.eval(parent, a, fl)
		}
	}
	return symbolic(f.text(n), "constructed object")
}

// callArgs evaluates positional arguments and the last-object options.
func (f *jsFrontend) callArgs(parent string, n *ts.Node, fl flow) (pos []model.Value, opts map[string]model.Value, raw []*ts.Node) {
	opts = map[string]model.Value{}
	args := f.field(n, "arguments")
	if args == nil {
		return
	}
	for _, a := range f.named(args) {
		if f.typ(a) == "comment" {
			continue
		}
		raw = append(raw, a)
		if f.typ(a) == "object" {
			for _, p := range f.named(a) {
				if f.typ(p) == "pair" {
					opts[strings.Trim(f.text(f.field(p, "key")), `"'`)] = f.eval(parent, f.field(p, "value"), fl)
				}
			}
			pos = append(pos, symbolic(f.text(a), "options object"))
			continue
		}
		if f.typ(a) == "arrow_function" || f.typ(a) == "function_expression" || f.typ(a) == "function" {
			pos = append(pos, symbolic("<callback>", "callback"))
			continue
		}
		pos = append(pos, f.eval(parent, a, fl))
	}
	return
}

// callback analyzes a function literal passed to a known call as a
// conditional body: it runs when the operation completes, if it does.
func (f *jsFrontend) callback(parent string, n *ts.Node, fl flow, why string) {
	if f.typ(n) != "arrow_function" && f.typ(n) != "function_expression" && f.typ(n) != "function" {
		return
	}
	id := f.container(parent, n, model.NodeFunction, "callback", fl.with(model.ReachConditional))
	f.b.Node(id).Command = &model.Command{Executable: model.Value{Knowledge: model.KnowledgeDerived, Text: "<callback>"}, Rule: "function", Assumptions: []string{why}}
	outer := f.scope
	f.scope = newScope(outer)
	defer func() { f.scope = outer }()
	if params := f.field(n, "parameters"); params != nil {
		for _, p := range f.named(params) {
			if f.typ(p) == "identifier" {
				f.scope.set(f.text(p), &binding{kind: bindValue, value: symbolic(f.text(p), "callback parameter")})
			}
		}
	} else if p := f.field(n, "parameter"); p != nil {
		f.scope.set(f.text(p), &binding{kind: bindValue, value: symbolic(f.text(p), "callback parameter")})
	}
	body := f.field(n, "body")
	if body == nil {
		return
	}
	if f.typ(body) == "statement_block" {
		f.block(id, body, fl.with(model.ReachConditional))
		return
	}
	f.eval(id, body, fl.with(model.ReachConditional))
}

func (f *jsFrontend) callExpr(parent string, n *ts.Node, fl flow) model.Value {
	fn := f.field(n, "function")
	if fn == nil {
		return model.Unknown()
	}
	// promise chains: x.then(cb).catch(cb)
	if f.typ(fn) == "member_expression" {
		prop := f.text(f.field(fn, "property"))
		if prop == "then" || prop == "catch" || prop == "finally" {
			f.eval(parent, f.field(fn, "object"), fl)
			if args := f.field(n, "arguments"); args != nil {
				for _, a := range f.named(args) {
					f.callback(parent, a, fl, "runs when the promise settles")
				}
			}
			return symbolic(f.text(n), "promise")
		}
	}
	qualified, ok := f.moduleRef(fn)
	if !ok {
		if f.typ(fn) == "identifier" {
			if b := f.scope.lookup(f.text(fn)); b != nil && b.kind == bindFunc {
				pos, _, raw := f.callArgs(parent, n, fl)
				_ = raw
				return f.userCall(parent, n, fn, b, pos, fl)
			} else if b != nil && b.shadowed {
				f.b.Diag(model.Diagnostic{Code: model.DiagShadowed, Severity: model.SeverityInfo, Message: "call to rebound name " + f.text(fn) + " is not the library function"})
			}
		}
		pos, _, raw := f.callArgs(parent, n, fl)
		_ = pos
		for _, a := range raw {
			f.callback(parent, a, fl, "callback passed to an unmodeled call; invocation timing unknown")
		}
		if f.typ(fn) == "member_expression" {
			f.eval(parent, f.field(fn, "object"), fl)
		}
		return symbolic(f.text(n), "call result is not modeled")
	}
	pos, opts, raw := f.callArgs(parent, n, fl)
	assume := qualified + " denotes the Node.js API"
	arg := func(i int) model.Value {
		if i < len(pos) {
			return pos[i]
		}
		return model.Value{}
	}
	// Normalize fs.promises.X and fs/promises.X to fs.X for dispatch.
	q := strings.TrimPrefix(qualified, "fs.promises.")
	if q != qualified {
		q = "fs." + q
	}
	base := strings.TrimSuffix(q, "Sync")
	emit := func(actions ...model.Action) {
		f.call(parent, n, qualified, pos, fl, assume, actions...)
		for _, a := range raw {
			f.callback(parent, a, fl, "callback runs when "+qualified+" completes")
		}
	}
	switch base {
	case "require":
		return symbolic(f.text(n), "module")
	case "path.join":
		if len(pos) == 0 {
			return model.Unknown()
		}
		v := pos[0]
		for _, more := range pos[1:] {
			v = joinPath(v, more)
		}
		return v
	case "path.resolve":
		v := f.cwd
		for _, more := range pos {
			v = joinPath(v, more)
		}
		return v
	case "path.dirname":
		if v := arg(0); v.Concrete() {
			return model.Value{Knowledge: model.KnowledgeDerived, Text: parentDir(v.Text)}
		}
		return symbolic(f.text(n), "dirname of non-literal")
	case "path.basename", "path.extname", "path.normalize", "path.relative":
		if v := arg(0); v.Concrete() && base == "path.normalize" {
			return v
		}
		return symbolic(f.text(n), "path component")
	case "process.cwd":
		return f.cwd
	case "process.chdir":
		id := f.call(parent, n, qualified, pos, fl, assume)
		target := arg(0)
		switch {
		case target.Concrete() && strings.HasPrefix(target.Text, "/"):
			f.cwd = model.Value{Knowledge: model.KnowledgeDerived, Text: target.Text, Assumptions: []string{"process.chdir succeeded"}}
		case target.Concrete() && f.cwd.Concrete():
			f.cwd = joinPath(f.cwd, target)
		default:
			f.cwd = model.Value{Knowledge: model.KnowledgeSymbolic, Text: target.Text}
		}
		f.b.Node(id).Context = f.nodeContext()
	case "process.exit", "process.on", "process.nextTick", "process.emitWarning":
	case "fs.readFile", "fs.createReadStream", "fs.readJson", "fs.readJSON":
		emit(model.Action{Kind: model.FSRead, Resources: []model.Resource{f.resource(arg(0), "target")}})
	case "fs.writeFile", "fs.createWriteStream", "fs.outputFile", "fs.writeJson", "fs.writeJSON":
		mode := model.WriteTruncate
		if fl, ok := opts["flag"]; ok && strings.Contains(fl.Text, "a") {
			mode = model.WriteAppend
		}
		emit(model.Action{Kind: model.FSWrite, Write: &model.WriteDetail{Mode: mode}, Resources: []model.Resource{f.resource(arg(0), "target")}})
	case "fs.appendFile":
		emit(model.Action{Kind: model.FSWrite, Write: &model.WriteDetail{Mode: model.WriteAppend}, Resources: []model.Resource{f.resource(arg(0), "target")}})
	case "fs.open":
		mode := arg(1)
		if fl, ok := opts["flags"]; ok {
			mode = fl
		}
		emit(f.fileActions(arg(0), mode, "r")...)
	case "fs.unlink", "fs.rmdir", "fs.remove":
		emit(model.Action{Kind: model.FSDelete, Delete: &model.DeleteDetail{Recursive: base == "fs.remove"}, Resources: []model.Resource{f.resource(arg(0), "target")}})
	case "fs.rm":
		d := &model.DeleteDetail{}
		if r, ok := opts["recursive"]; ok && r.Text == "true" {
			d.Recursive = true
		}
		if fo, ok := opts["force"]; ok && fo.Text == "true" {
			d.Force = true
		}
		emit(model.Action{Kind: model.FSDelete, Delete: d, Resources: []model.Resource{f.resource(arg(0), "target")}})
	case "fs.mkdir", "fs.ensureDir", "fs.mkdirp", "fs.mkdtemp":
		mode := model.WriteCreateExclusive
		if r, ok := opts["recursive"]; ok && r.Text == "true" || base != "fs.mkdir" {
			mode = model.WriteUnknown
		}
		emit(model.Action{Kind: model.FSWrite, Write: &model.WriteDetail{Mode: mode, Directory: true}, Resources: []model.Resource{f.resource(arg(0), "target")}})
	case "fs.readdir", "fs.opendir":
		emit(model.Action{Kind: model.FSList, Resources: []model.Resource{f.resource(arg(0), "scope")}})
	case "fs.stat", "fs.lstat", "fs.access", "fs.exists", "fs.pathExists", "fs.chmod", "fs.chown", "fs.utimes", "fs.realpath", "fs.watch", "fs.watchFile":
		emit(model.Action{Kind: model.FSMetadata, Resources: []model.Resource{f.resource(arg(0), "target")}})
		return symbolic(f.text(n), "metadata result")
	case "fs.copyFile", "fs.cp", "fs.copy":
		emit(model.Action{Kind: model.FSCopy, Resources: []model.Resource{f.resource(arg(0), "source"), f.resource(arg(1), "destination")}})
	case "fs.rename", "fs.move":
		emit(model.Action{Kind: model.FSMove, Resources: []model.Resource{f.resource(arg(0), "source"), f.resource(arg(1), "destination")}})
	case "fs.symlink", "fs.link":
		emit(model.Action{Kind: model.FSWrite, Write: &model.WriteDetail{Mode: model.WriteCreateExclusive}, Resources: []model.Resource{f.resource(arg(1), "target"), f.resource(arg(0), "source")}})
	case "fs.truncate":
		emit(model.Action{Kind: model.FSWrite, Write: &model.WriteDetail{Mode: model.WriteTruncate}, Resources: []model.Resource{f.resource(arg(0), "target")}})
	case "child_process.exec":
		var cwd *model.Value
		if c, ok := opts["cwd"]; ok {
			cwd = &c
		}
		f.spawn(parent, n, fl, qualified, arg(0), nil, true, cwd)
		for _, a := range raw {
			f.callback(parent, a, fl, "callback runs when the child process exits")
		}
	case "child_process.execFile", "child_process.spawn":
		var argv []model.Value
		if len(raw) > 1 && f.typ(raw[1]) == "array" {
			for _, e := range f.named(raw[1]) {
				argv = append(argv, f.eval(parent, e, fl))
			}
		}
		shell := false
		if s, ok := opts["shell"]; ok && (s.Text == "true" || (s.Concrete() && s.Text != "false")) {
			shell = true
		}
		var cwd *model.Value
		if c, ok := opts["cwd"]; ok {
			cwd = &c
		}
		f.spawn(parent, n, fl, qualified, arg(0), argv, shell, cwd)
		for _, a := range raw {
			f.callback(parent, a, fl, "callback runs when the child process exits")
		}
	case "child_process.fork":
		file := f.resource(arg(0), "target")
		id := f.call(parent, n, qualified, pos, fl, assume, model.Action{Kind: model.ProcessInterpreter, Resources: []model.Resource{file}, Process: &model.ProcessDetail{Executable: model.Literal("node"), Args: []model.Value{arg(0)}}})
		f.b.Diag(model.Diagnostic{Code: model.DiagMissingSnapshot, Severity: model.SeverityInfo, Node: id, Message: "forked module contents are not part of the invocation"})
	default:
		if strings.HasPrefix(qualified, "fs.") || strings.HasPrefix(qualified, "child_process.") {
			id := f.call(parent, n, qualified, pos, fl, assume)
			f.b.Node(id).Detail = "library call without modeled effects"
			f.b.Diag(model.Diagnostic{Code: model.DiagUnsupported, Severity: model.SeverityInfo, Node: id, Message: qualified + " is not modeled"})
		}
		for _, a := range raw {
			f.callback(parent, a, fl, "callback passed to "+qualified)
		}
	}
	return symbolic(f.text(n), "call result")
}

func (f *jsFrontend) userCall(parent string, n, fn *ts.Node, b *binding, args []model.Value, fl flow) model.Value {
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
			pname := ""
			switch f.typ(p) {
			case "identifier":
				pname = f.text(p)
			case "assignment_pattern":
				if l := f.field(p, "left"); l != nil {
					pname = f.text(l)
				}
			default:
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
