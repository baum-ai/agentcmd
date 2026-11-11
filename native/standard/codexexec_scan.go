package standard

import (
	"path"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/baum-ai/agentcmd/model"
)

// This file holds a small dependency-free JavaScript scanner for Codex's
// code-mode tool `exec`, whose input is a JavaScript program that calls
// other tools: `tools.exec_command({cmd: "...", workdir: "..."})`. The scan
// is lexical (no parse tree): it finds calls of known tools and, when the
// first argument is an object literal, reads its properties. Nothing is
// evaluated. Control flow is not modeled; the nesting of a call (inside a
// block, after `&&`, inside a function body or parameter list, in a loop
// head) only qualifies its reachability.

// maxCodexExecCalls bounds the commands taken from one program.
const maxCodexExecCalls = 256

// maxTemplateDepth bounds nested template-literal substitutions.
const maxTemplateDepth = 32

// stepsPerToken bounds the work scanning a program does: the tokens of every
// object scanned, together, may not exceed this multiple of the program's
// tokens, so adversarial nesting stays linear.
const stepsPerToken = 16

// codexExecCall is one exec_command or shell call found in a program.
type codexExecCall struct {
	// Callee is "exec_command" or "shell".
	Callee string
	// Command is the shell text; CommandStatic is false when it is built at
	// run time, or when a spread, computed or duplicate key could change it.
	// Reason then says why.
	Command       string
	CommandStatic bool
	Reason        string
	// Workdir is the literal directory; HasWorkdir says the object names
	// one, WorkdirStatic that its value is a non-empty string literal.
	Workdir                   string
	HasWorkdir, WorkdirStatic bool
	// Start and End delimit the argument in the program source; CmdStart and
	// CmdEnd the command's value (zero when there is none).
	Start, End       int
	CmdStart, CmdEnd int
	Reach            model.Reachability
}

// codexApplyPatch is an `apply_patch(...)` call.
type codexApplyPatch struct {
	// Patch is the text when Static; otherwise the argument is computed.
	Patch      string
	Static     bool
	Start, End int
	Reach      model.Reachability
}

// codexRef is a reference to a tool of the tools object that is not a direct
// call (`const run = tools.exec_command`, `tools[name](...)`): the program may
// call it in ways the scan does not follow.
type codexRef struct {
	Name       string
	Start, End int
	Reach      model.Reachability
}

// codexScan is the outcome of scanning one program.
type codexScan struct {
	calls   []codexExecCall
	patches []codexApplyPatch
	refs    []codexRef
	// truncated: more than maxCodexExecCalls calls. exhausted: the step
	// budget ran out, so later calls were not examined. tooDeep: template
	// literals nested beyond maxTemplateDepth. unbalanced: the brackets do
	// not pair up (not valid JavaScript). unterminated: a string, template
	// literal or comment runs to the end of the program.
	truncated, exhausted, tooDeep, unbalanced, unterminated bool
}

// frame kinds of the nesting stack.
const (
	frParen    = iota // ( ... )
	frBracket         // [ ... ]
	frObject          // { ... } object literal
	frBlock           // { ... } statement block
	frLoop            // { ... } loop body
	frFunc            // { ... } function or arrow body
	frArrow           // expression body of an arrow function, up to , ; or a closer
	frCond            // rest of an expression after && || ?? or ?
	frStmt            // unbraced body of if/else, up to ;
	frLoopStmt        // unbraced body of for/while, up to ;
	frParams          // ( ... ) parameter list of a function, method or arrow
	frLoopHead        // ( ... ) head of a for or while
)

type frame struct{ kind int }

var reachByRank = [...]model.Reachability{model.ReachAlways, model.ReachConditional, model.ReachRepeated, model.ReachLatent}

// reachOf is the weakest reachability of the frames on the stack.
func reachOf(stack []frame) model.Reachability {
	rank := 0
	for _, f := range stack {
		r := 0
		switch f.kind {
		case frBlock, frCond, frStmt:
			r = 1
		case frLoop, frLoopStmt, frLoopHead:
			r = 2
		case frFunc, frArrow, frParams:
			r = 3
		}
		if r > rank {
			rank = r
		}
	}
	return reachByRank[rank]
}

// scanCodexExec scans a code-mode program.
func scanCodexExec(source string) codexScan {
	lx := &lexer{src: source}
	lx.run(0, false)
	// A leading sentinel lets readers look back without bounds checks.
	tokens := append([]token{{kind: tokPunct, text: ";"}}, lx.toks...)
	var out codexScan
	out.tooDeep, out.unterminated = lx.tooDeep, lx.unterminated
	match, ok := matchBrackets(tokens)
	out.unbalanced = !ok
	declared := declaredNames(tokens)
	budget := stepsPerToken * len(tokens)
	var stack []frame
	for i := 1; i < len(tokens); i++ {
		if budget <= 0 {
			out.exhausted = true
			break
		}
		budget--
		if endsStatementBefore(tokens, i) {
			stack = popExpr(stack, true)
		}
		reachHere := func() model.Reachability {
			r := reachOf(stack)
			if r == model.ReachAlways && tokens[i].weak {
				r = model.ReachConditional
			}
			return r
		}
		if name, end, ok := toolReference(tokens, i, match); ok {
			if len(out.refs)+len(out.calls)+len(out.patches) >= maxCodexExecCalls {
				out.truncated = true
				break
			}
			out.refs = append(out.refs, codexRef{Name: name, Start: tokens[i].start, End: end, Reach: reachHere()})
		}
		if callee, isCall := callTarget(tokens, i, match, declared); isCall {
			if len(out.refs)+len(out.calls)+len(out.patches) >= maxCodexExecCalls {
				out.truncated = true
				break
			}
			reach := reachHere()
			if callee == "apply_patch" {
				p := codexApplyPatch{Start: tokens[i].start, End: argEnd(tokens, i), Reach: reach}
				if tokens[i].kind == tokString && tokens[i].static && i+1 < len(tokens) && (tokens[i+1].isPunct(")") || tokens[i+1].isPunct(",")) {
					p.Static, p.Patch = true, tokens[i].value
				}
				out.patches = append(out.patches, p)
			} else {
				call, spent := scanObject(tokens, i, callee, match)
				budget -= spent
				call.Reach = reach
				out.calls = append(out.calls, call)
			}
		}
		stack = applyToken(stack, tokens, i, match)
	}
	return out
}

// toolNames are the tools whose calls the scan reads.
var toolNames = map[string]bool{"exec_command": true, "shell": true, "apply_patch": true}

// callTarget reports whether tokens[i] starts the first argument of a call of
// exec_command, shell or apply_patch: `name(`, `tools.name(`, `tools?.name(`
// or `tools.name?.(`. A bare name the program declares itself, and a function
// or method definition with that name, are not calls.
func callTarget(tokens []token, i int, match []int, declared map[string]bool) (string, bool) {
	if i < 3 || !tokens[i-1].isPunct("(") {
		return "", false
	}
	n := i - 2
	if n >= 3 && tokens[n].isPunct(".") && tokens[n-1].isOperator("?") {
		n -= 2 // name?.(
	}
	if tokens[n].kind != tokIdent || !toolNames[tokens[n].value] {
		return "", false
	}
	name := tokens[n].value
	if tokens[n-1].isPunct(".") {
		q := n - 2
		if q >= 1 && tokens[q].isOperator("?") {
			q--
		}
		if q < 1 || tokens[q].kind != tokIdent || tokens[q].value != "tools" {
			return "", false // some other object's method
		}
		if tokens[q-1].isPunct(".") && !(q >= 3 && tokens[q-2].isPunct(".") && tokens[q-3].isPunct(".")) {
			return "", false // other.tools.name(: a member of another object, but not ...tools
		}
	} else if declared[name] {
		return "", false // the program defines its own function of that name
	}
	if tokens[n-1].kind == tokIdent && tokens[n-1].value == "function" || tokens[n-1].isOperator("*") && n >= 2 && tokens[n-2].value == "function" {
		return "", false // function exec_command(...)
	}
	if tokens[i].isPunct(")") {
		return "", false // no argument
	}
	if m := match[i-1]; m > 0 && m+1 < len(tokens) && tokens[m+1].isPunct("{") {
		return "", false // method definition: name(...) { ... }
	}
	return name, true
}

// toolReference reports a use of the tools object at tokens[i] that the scan
// does not follow: tools.exec_command, tools.shell or tools.apply_patch that
// is not a direct call, a computed tools[...], or `tools` itself used as a
// value (`const t = tools`, an argument, a renaming or rest destructure). It
// returns a description and the end offset of the reference. Not references:
// a destructure made only of the three tool names (`const {exec_command} =
// tools`), and `const NAME = tools.NAME` for one of them, which the scan
// follows by name.
func toolReference(tokens []token, i int, match []int) (string, int, bool) {
	if tokens[i].kind != tokIdent || tokens[i].value != "tools" {
		return "", 0, false
	}
	if tokens[i-1].isPunct(".") && !(i >= 3 && tokens[i-2].isPunct(".") && tokens[i-3].isPunct(".")) {
		return "", 0, false // x.tools, but not ...tools
	}
	at := func(k int) token {
		if k < len(tokens) {
			return tokens[k]
		}
		return token{}
	}
	a := i + 1
	switch {
	case at(a).isOperator("?") && at(a+1).isPunct("."):
		a += 2
	case at(a).isPunct("."):
		a++
	case at(a).isPunct("["):
	default:
		return toolsValue(tokens, i, match)
	}
	if at(a).isPunct("[") {
		if at(a+1).kind == tokString && at(a+1).static && !toolNames[at(a+1).value] && at(a+2).isPunct("]") {
			return "", 0, false // some other tool, by a literal name
		}
		return "tools[...]", tokens[min(a, len(tokens)-1)].end, true
	}
	if at(a).kind != tokIdent || !toolNames[at(a).value] {
		return "", 0, false
	}
	name := at(a).value
	if at(a+1).isPunct("(") || at(a+1).isOperator("?") && at(a+2).isPunct(".") && at(a+3).isPunct("(") {
		return "", 0, false // a direct call
	}
	if i >= 3 && tokens[i-1].isOperator("=") && tokens[i-2].kind == tokIdent && tokens[i-2].value == name &&
		tokens[i-3].kind == tokIdent && (tokens[i-3].value == "const" || tokens[i-3].value == "let" || tokens[i-3].value == "var") {
		return "", 0, false // const exec_command = tools.exec_command
	}
	return "tools." + name, at(a).end, true
}

// toolsValue handles `tools` not followed by a member access: a use of the
// object as a value, unless it is a declaration, a property key or the
// destructure of tool names.
func toolsValue(tokens []token, i int, match []int) (string, int, bool) {
	prev := tokens[i-1]
	next := token{}
	if i+1 < len(tokens) {
		next = tokens[i+1]
	}
	switch {
	case prev.kind == tokIdent && (prev.value == "const" || prev.value == "let" || prev.value == "var" || prev.value == "function" || prev.value == "class"):
		return "", 0, false // the program's own variable named tools
	case (prev.isPunct("{") || prev.isPunct(",")) && next.isPunct(":"):
		return "", 0, false // a property key
	case prev.isOperator("=") && i >= 2 && tokens[i-2].isPunct("}") && match[i-2] > 0 && toolNamesOnly(tokens[match[i-2]+1:i-2]):
		return "", 0, false // const {exec_command, shell} = tools
	}
	return "tools", tokens[i].end, true
}

// toolNamesOnly reports whether the tokens are shorthand tool names
// separated by commas, the inside of `{exec_command, shell}`.
func toolNamesOnly(toks []token) bool {
	if len(toks) == 0 {
		return false
	}
	for k, t := range toks {
		if k%2 == 0 {
			if t.kind != tokIdent || !toolNames[t.value] {
				return false
			}
		} else if !t.isPunct(",") {
			return false
		}
	}
	return true
}

// declaredNames returns the tool names the program declares itself
// (`function shell`, `class apply_patch`, `const exec_command = ...`), so a
// bare call of them is the program's own function. `const exec_command =
// tools.exec_command` is the tool itself.
func declaredNames(tokens []token) map[string]bool {
	out := map[string]bool{}
	named := func(k int) (string, bool) {
		if k < len(tokens) && tokens[k].kind == tokIdent && toolNames[tokens[k].value] {
			return tokens[k].value, true
		}
		return "", false
	}
	for i := 1; i < len(tokens); i++ {
		t := tokens[i]
		if t.kind != tokIdent {
			continue
		}
		switch t.value {
		case "function":
			j := i + 1
			if j < len(tokens) && tokens[j].isOperator("*") {
				j++
			}
			if n, ok := named(j); ok {
				out[n] = true
			}
		case "class":
			if n, ok := named(i + 1); ok {
				out[n] = true
			}
		case "const", "let", "var":
			if n, ok := named(i + 1); ok && i+2 < len(tokens) && tokens[i+2].isOperator("=") {
				alias := i+6 < len(tokens) && tokens[i+3].kind == tokIdent && tokens[i+3].value == "tools" && tokens[i+4].isPunct(".") &&
					tokens[i+5].kind == tokIdent && tokens[i+5].value == n && (tokens[i+6].isPunct(";") || tokens[i+6].isPunct(",") || tokens[i+6].nl)
				if !alias {
					out[n] = true
				}
			}
		}
	}
	return out
}

var pairs = map[string]string{"(": ")", "[": "]", "{": "}"}

// matchBrackets pairs every bracket with its partner (index, or -1) and
// reports whether all of them pair up.
func matchBrackets(tokens []token) ([]int, bool) {
	match := make([]int, len(tokens))
	for i := range match {
		match[i] = -1
	}
	var stack []int
	ok := true
	for i, t := range tokens {
		if t.kind != tokPunct {
			continue
		}
		switch t.text {
		case "(", "[", "{":
			stack = append(stack, i)
		case ")", "]", "}":
			if len(stack) == 0 {
				ok = false
				continue
			}
			o := stack[len(stack)-1]
			if pairs[tokens[o].text] != t.text {
				ok = false
				continue
			}
			stack = stack[:len(stack)-1]
			match[o], match[i] = i, o
		}
	}
	return match, ok && len(stack) == 0
}

// popExpr pops the frames of the current expression and statement: arrow and
// conditional tails and, when stmt is set, unbraced if/else/loop bodies. A
// comma ends only the first kind; `;`, a newline that ends a statement and a
// closing bracket end both.
func popExpr(stack []frame, stmt bool) []frame {
	for len(stack) > 0 {
		switch stack[len(stack)-1].kind {
		case frArrow, frCond:
			stack = stack[:len(stack)-1]
			continue
		case frStmt, frLoopStmt:
			if stmt {
				stack = stack[:len(stack)-1]
				continue
			}
		}
		break
	}
	return stack
}

// paramsOpen reports whether the "(" at tokens[i] opens a parameter list:
// of a function, of a method definition (`name(...) {`) or of an arrow
// (`(...) =>`). Default values in it run only when the function is called.
func paramsOpen(tokens []token, i int, match []int) bool {
	t := tokens[i]
	if t.fn {
		return true
	}
	m := match[i]
	if m <= 0 || m+1 >= len(tokens) {
		return false
	}
	if t.meth && tokens[m+1].isPunct("{") {
		return true
	}
	return tokens[m+1].isOperator("=") && m+2 < len(tokens) && tokens[m+2].isOperator(">")
}

// applyToken updates the nesting stack for tokens[i].
func applyToken(stack []frame, tokens []token, i int, match []int) []frame {
	t := tokens[i]
	prev := tokens[i-1]
	switch {
	case t.isPunct("("):
		switch {
		case paramsOpen(tokens, i, match):
			return append(stack, frame{frParams})
		case t.loop:
			// The test and update of a for or while may run any number of
			// times, including none; the initializer is weakened with them.
			return append(stack, frame{frLoopHead})
		}
		return append(stack, frame{frParen})
	case t.isPunct("["):
		return append(stack, frame{frBracket})
	case t.isPunct("{"):
		kind := frObject
		switch {
		case prev.isPunct(")") && (prev.fn || prev.meth), prev.isOperator(">") && tokens[i-2].isOperator("="):
			kind = frFunc
		case prev.isPunct(")") && prev.loop, prev.kind == tokIdent && prev.value == "do":
			kind = frLoop
		case prev.isPunct(")"), prev.isPunct(";"), prev.isPunct("}"),
			prev.kind == tokIdent && (prev.value == "else" || prev.value == "try" || prev.value == "finally"):
			kind = frBlock
		case prev.isPunct("{"):
			if len(stack) > 0 && stack[len(stack)-1].kind != frObject {
				kind = frBlock
			}
		case prev.kind == tokIdent && !regexKeywords[prev.value]:
			kind = frBlock // class A { ... }
		}
		return append(stack, frame{kind})
	case t.isPunct(")"), t.isPunct("]"), t.isPunct("}"):
		stack = popExpr(stack, true)
		closed := -1
		if len(stack) > 0 {
			closed = stack[len(stack)-1].kind
			stack = stack[:len(stack)-1]
		}
		if closed == frBlock || closed == frLoop {
			// The block ends the unbraced if/else/for/while body it was.
			stack = popExpr(stack, true)
		}
		// An unbraced statement body after if(...), for(...) or while(...).
		if t.isPunct(")") && t.ctrl && i+1 < len(tokens) && !tokens[i+1].isPunct("{") && !tokens[i+1].isPunct(";") {
			if t.loop {
				return append(stack, frame{frLoopStmt})
			}
			return append(stack, frame{frStmt})
		}
		return stack
	case t.isPunct(","):
		return popExpr(stack, false)
	case t.isPunct(";"):
		return popExpr(stack, true)
	case t.kind == tokIdent && t.value == "else" && i+1 < len(tokens) && !tokens[i+1].isPunct("{"):
		return append(stack, frame{frStmt})
	case t.kind == tokIdent && t.value == "do" && i+1 < len(tokens) && !tokens[i+1].isPunct("{"):
		return append(stack, frame{frLoopStmt})
	case t.isOperator(">") && prev.isOperator("="):
		if i+1 < len(tokens) && !tokens[i+1].isPunct("{") {
			return append(stack, frame{frArrow})
		}
	case t.isOperator("?"):
		if i+1 < len(tokens) && tokens[i+1].isPunct(".") {
			return stack // optional chaining
		}
		return append(stack, frame{frCond})
	case t.isOperator("&") && prev.isOperator("&"), t.isOperator("|") && prev.isOperator("|"):
		return append(stack, frame{frCond})
	}
	return stack
}

// noEnd are identifiers after which a newline does not end a statement: they
// need an operand, or start a construct that continues on the next line.
var noEnd = map[string]bool{
	"return": true, "typeof": true, "case": true, "in": true, "of": true, "delete": true, "void": true, "throw": true,
	"new": true, "await": true, "yield": true, "else": true, "do": true, "instanceof": true, "function": true, "class": true,
	"async": true, "const": true, "let": true, "var": true, "if": true, "while": true, "for": true, "with": true,
	"switch": true, "catch": true, "try": true, "finally": true, "import": true, "export": true, "extends": true,
	"static": true, "get": true, "set": true,
}

// continues are identifiers that continue the expression on the previous
// line when they start a line.
var continues = map[string]bool{"in": true, "of": true, "instanceof": true, "else": true, "catch": true, "finally": true}

// endsStatementBefore reports whether a newline before tokens[i] ends the
// statement of the previous token (automatic semicolon insertion): the line
// ended on an operand and the next one starts with an identifier that cannot
// continue it. Anything doubtful keeps the statement open, which only makes
// reachability weaker, never stronger.
func endsStatementBefore(tokens []token, i int) bool {
	t, prev := tokens[i], tokens[i-1]
	if !t.nl || t.kind != tokIdent || continues[t.value] {
		return false
	}
	switch prev.kind {
	case tokIdent:
		return !noEnd[prev.value]
	case tokString, tokRegex:
		return true
	case tokPunct:
		return prev.text == "]" || prev.text == "}" || prev.text == ")" && !prev.ctrl
	}
	return false
}

// scanObject reads the argument of an exec_command or shell call. It returns
// the call and the number of tokens examined.
func scanObject(tokens []token, i int, callee string, match []int) (codexExecCall, int) {
	call := codexExecCall{Callee: callee, Start: tokens[i].start, End: tokens[i].end}
	if !tokens[i].isPunct("{") {
		call.Reason = "the argument of the code-mode " + callee + " call is not an object literal"
		call.End = argEnd(tokens, i)
		return call, 1
	}
	closeIdx := match[i]
	if closeIdx < 0 {
		call.Reason = "unterminated argument of the code-mode " + callee + " call"
		return call, 1
	}
	call.End = tokens[closeIdx].end
	if closeIdx+1 < len(tokens) && !tokens[closeIdx+1].isPunct(",") && !tokens[closeIdx+1].isPunct(")") {
		call.Reason = "the argument of the code-mode " + callee + " call is an expression, not an object literal"
		return call, 1
	}
	keys := "cmd"
	if callee == "shell" {
		keys = "command"
	}
	var seenCmd, seenDir, dup bool
	opaque := ""
	depth := 0
	steps := 0
	for j := i + 1; j < closeIdx; j++ {
		steps++
		t := tokens[j]
		atProperty := depth == 0 && (tokens[j-1].isPunct("{") || tokens[j-1].isPunct(","))
		switch {
		case t.isPunct("{"), t.isPunct("["), t.isPunct("("):
			if atProperty && t.text == "[" {
				opaque = "a computed key"
			}
			depth++
		case t.isPunct("}"), t.isPunct("]"), t.isPunct(")"):
			depth--
		case atProperty && t.isPunct("."):
			opaque = "a spread"
		case atProperty && t.isKey() && j+2 < len(tokens) && tokens[j+1].isPunct(":"):
			switch t.value {
			case keys:
				if seenCmd {
					dup = true
				}
				seenCmd = true
				if !dup {
					call.Command, call.CommandStatic = commandValue(tokens, j+2, callee == "shell")
					call.CmdStart, call.CmdEnd = tokens[j+2].start, tokens[j+2].end
				}
			case "__proto__":
				opaque = "a __proto__ property"
			case "workdir":
				if seenDir {
					dup = true
				}
				seenDir = true
				call.HasWorkdir = true
				value := tokens[j+2]
				call.WorkdirStatic = value.kind == tokString && value.static && value.value != "" && endsValue(tokens, j+3)
				call.Workdir = ""
				if call.WorkdirStatic {
					call.Workdir = value.value
				}
			}
		case atProperty && t.kind == tokIdent && j+1 < len(tokens) && (tokens[j+1].isPunct(",") || tokens[j+1].isPunct("}")):
			// Shorthand: the value is a variable.
			switch t.value {
			case keys:
				if seenCmd {
					dup = true
				}
				seenCmd = true
				call.CommandStatic = false
			case "workdir":
				if seenDir {
					dup = true
				}
				seenDir = true
				call.HasWorkdir, call.WorkdirStatic = true, false
			}
		case atProperty && (t.kind == tokIdent || t.kind == tokString) && j+1 < len(tokens) && !tokens[j+1].isPunct(":") && !tokens[j+1].isPunct(",") && !tokens[j+1].isPunct("}"):
			opaque = "a getter, method or other property form"
		}
	}
	switch {
	case dup:
		call.CommandStatic, call.Reason = false, "the object of the code-mode "+callee+" call repeats a property, so the last one wins at run time"
	case opaque != "":
		call.CommandStatic, call.Reason = false, "the object of the code-mode "+callee+" call has "+opaque+" that may set the command"
	case !seenCmd:
		call.CommandStatic, call.Reason = false, "the object of the code-mode "+callee+" call has no static "+keys+" property"
	case !call.CommandStatic:
		call.Reason = "the " + keys + " of the code-mode " + callee + " call is not a static string"
	}
	return call, steps
}

// argEnd returns the end offset of the argument starting at tokens[i], up to
// its closing `,` or `)` at the same depth (at most 64 tokens).
func argEnd(tokens []token, i int) int {
	depth := 0
	end := tokens[i].end
	for j := i; j < len(tokens) && j < i+64; j++ {
		t := tokens[j]
		if t.kind == tokPunct {
			switch t.text {
			case "(", "[", "{":
				depth++
			case ")", "]", "}":
				if depth == 0 {
					return end
				}
				depth--
			case ",":
				if depth == 0 {
					return end
				}
			}
		}
		end = t.end
	}
	return end
}

// endsValue reports whether the property value that ended before tokens[i]
// is complete: the next token closes or separates properties, so the value
// is not the start of an expression such as `"a" + b` or `"a" ? x : y`.
func endsValue(tokens []token, i int) bool {
	return i >= len(tokens) || tokens[i].isPunct(",") || tokens[i].isPunct("}")
}

// commandValue reads the command a property value at tokens[i] holds: a
// string literal, or (argv true) an argv array of them.
func commandValue(tokens []token, i int, argv bool) (string, bool) {
	first := tokens[i]
	switch {
	case first.kind == tokString:
		if argv || !first.static || first.value == "" || !endsValue(tokens, i+1) {
			return "", false
		}
		return first.value, true
	case argv && first.isPunct("["):
		var words []string
		for j := i + 1; j < len(tokens); j += 2 {
			if tokens[j].kind != tokString || !tokens[j].static || j+1 >= len(tokens) {
				return "", false
			}
			words = append(words, tokens[j].value)
			switch {
			case tokens[j+1].isPunct("]"):
				if !endsValue(tokens, j+2) {
					return "", false
				}
				return argvCommand(words)
			case !tokens[j+1].isPunct(","):
				return "", false
			}
		}
	}
	return "", false
}

// argvCommand turns an argv into the command line it runs: the script of a
// `bash -lc script` call, else the words each single-quoted and joined, which
// is the same argv without a guess about word splitting.
func argvCommand(argv []string) (string, bool) {
	if len(argv) == 0 {
		return "", false
	}
	if len(argv) >= 3 && isShellCommand(argv[0], argv[1]) {
		return argv[2], argv[2] != ""
	}
	quoted := make([]string, len(argv))
	for i, w := range argv {
		quoted[i] = "'" + strings.ReplaceAll(w, "'", `'\''`) + "'"
	}
	return strings.Join(quoted, " "), true
}

func isShellCommand(executable, flags string) bool {
	switch path.Base(executable) {
	case "bash", "sh", "zsh":
		return strings.HasPrefix(flags, "-") && strings.Contains(strings.TrimLeft(flags, "-"), "c")
	}
	return false
}

type tokenKind int

const (
	tokIdent tokenKind = iota
	tokString
	tokRegex
	tokPunct
	tokOperator
)

type token struct {
	kind   tokenKind
	text   string // punctuation and operators
	value  string // identifier name or decoded string
	static bool
	// start and end are the byte range of the token in the program source.
	start, end int
	// For a ")": ctrl closes the head of an if, while, for, with or switch (a
	// regular expression may follow), loop the head of a for or while, fn a
	// function parameter list. A "(" carries loop and fn too.
	ctrl, loop, fn bool
	// meth marks the ")" of a method definition's parameters (`name(...) {`),
	// and the "(" that may open one.
	meth bool
	// nl: a line break precedes the token. weak: the token is inside an
	// unterminated string or template literal.
	nl, weak bool
}

func (t token) isKey() bool {
	return (t.kind == tokIdent || t.kind == tokString) && t.static
}
func (t token) isPunct(text string) bool    { return t.kind == tokPunct && t.text == text }
func (t token) isOperator(text string) bool { return t.kind == tokOperator && t.text == text }

// regexKeywords are the identifiers after which a slash starts a regular
// expression rather than dividing.
var regexKeywords = map[string]bool{
	"return": true, "typeof": true, "case": true, "in": true, "of": true, "delete": true,
	"void": true, "throw": true, "new": true, "await": true, "yield": true, "else": true, "do": true,
}

// lexer splits JavaScript source into identifiers, string literals, regular
// expressions, punctuation and operators. Whitespace and comments are
// dropped. It is approximate (no automatic semicolons) and made to keep
// quotes inside strings, comments and regular expressions from
// desynchronising the scan. Template literal substitutions are lexed in
// place, so calls inside them are seen.
type lexer struct {
	src     string
	toks    []token
	parens  []token // flags of the open parentheses
	depth   int
	tooDeep bool
	// nl is set after a line break until the next token is added.
	nl bool
	// unterminated is set when a string, template literal or comment runs to
	// the end of the program.
	unterminated bool
}

// add appends a token, recording a preceding line break.
func (l *lexer) add(t token) int {
	t.nl, l.nl = l.nl, false
	l.toks = append(l.toks, t)
	return len(l.toks) - 1
}

// regexAllowed reports whether a slash after the tokens so far starts a
// regular expression rather than dividing.
func regexAllowed(toks []token) bool {
	if len(toks) == 0 {
		return true
	}
	prev := toks[len(toks)-1]
	switch prev.kind {
	case tokIdent:
		return regexKeywords[prev.value]
	case tokString, tokRegex:
		return false
	case tokPunct:
		switch prev.text {
		case ")":
			return prev.ctrl
		case "]":
			return false
		}
		return true
	case tokOperator:
		// A postfix ++ or -- ends an operand: the slash divides.
		if (prev.text == "+" || prev.text == "-") && len(toks) >= 3 && toks[len(toks)-2].isOperator(prev.text) && operandEnd(toks[len(toks)-3]) {
			return false
		}
	}
	return true
}

// operandEnd reports whether t can end an operand.
func operandEnd(t token) bool {
	switch t.kind {
	case tokIdent:
		return !regexKeywords[t.value]
	case tokString, tokRegex:
		return true
	case tokPunct:
		return t.text == "]" || (t.text == ")" && !t.ctrl)
	}
	return false
}

// parenFlags classifies the parenthesis about to open after the tokens so far.
func parenFlags(toks []token) token {
	var open token
	n := len(toks)
	if n == 0 {
		return open
	}
	prev := toks[n-1]
	isFunction := func(k int) bool { return k >= 0 && toks[k].kind == tokIdent && toks[k].value == "function" }
	switch {
	case prev.kind == tokIdent && (prev.value == "if" || prev.value == "with" || prev.value == "switch"):
		open.ctrl = true
	case prev.kind == tokIdent && (prev.value == "while" || prev.value == "for"):
		open.ctrl, open.loop = true, true
	case prev.kind == tokIdent && prev.value == "await" && n > 1 && toks[n-2].kind == tokIdent && toks[n-2].value == "for":
		open.ctrl, open.loop = true, true
	case prev.kind == tokIdent && !noEnd[prev.value] && !regexKeywords[prev.value] && n > 1 && methodContext(toks[n-2]):
		open.meth = true
	case isFunction(n - 1), prev.kind == tokIdent && isFunction(n-2), prev.kind == tokIdent && n > 2 && toks[n-2].isOperator("*") && isFunction(n-3), prev.isOperator("*") && isFunction(n-2):
		open.fn = true
	}
	return open
}

// methodContext reports whether t can precede the name of a method
// definition in an object literal or class body.
func methodContext(t token) bool {
	switch t.kind {
	case tokPunct:
		return t.text == "{" || t.text == "," || t.text == ";" || t.text == "}"
	case tokIdent:
		return t.value == "static" || t.value == "async" || t.value == "get" || t.value == "set"
	case tokOperator:
		return t.text == "*"
	}
	return false
}

// run lexes from pos. In substitution mode it stops after the `}` that closes
// the substitution and returns the position after it.
func (l *lexer) run(pos int, subst bool) int {
	src := l.src
	braces := 0
	for pos < len(src) {
		c := src[pos]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			if c == '\n' || c == '\r' {
				l.nl = true
			}
			pos++
		case c == '/' && pos+1 < len(src) && src[pos+1] == '/':
			for pos < len(src) && src[pos] != '\n' {
				pos++
			}
		case c == '/' && pos+1 < len(src) && src[pos+1] == '*':
			end := strings.Index(src[pos+2:], "*/")
			if end < 0 {
				l.unterminated = true
				return len(src)
			}
			if strings.ContainsAny(src[pos:pos+2+end], "\n\r") {
				l.nl = true
			}
			pos += 2 + end + 2
		case c == '"' || c == '\'' || c == '`':
			idx := l.add(token{kind: tokString, start: pos})
			was := l.unterminated
			value, static, next := l.lexString(pos)
			l.toks[idx].value, l.toks[idx].static, l.toks[idx].end = value, static, next
			if l.unterminated && !was {
				// The literal never closes: what looks like code inside it
				// is not known to run.
				for k := idx; k < len(l.toks); k++ {
					l.toks[k].weak = true
				}
			}
			pos = next
		case c == '/' && regexAllowed(l.toks):
			if next, ok := scanRegex(src, pos); ok {
				l.add(token{kind: tokRegex, start: pos, end: next})
				pos = next
				break
			}
			l.add(token{kind: tokOperator, text: "/", start: pos, end: pos + 1})
			pos++
		case isIdentByte(c) || (c == '\\' && pos+1 < len(src) && src[pos+1] == 'u'):
			start := pos
			var sb strings.Builder
			for pos < len(src) {
				if isIdentByte(src[pos]) {
					sb.WriteByte(src[pos])
					pos++
				} else if src[pos] == '\\' && pos+1 < len(src) && src[pos+1] == 'u' {
					pos = decodeEscape(src, pos, &sb)
				} else {
					break
				}
			}
			word := sb.String()
			l.add(token{kind: tokIdent, text: word, value: word, static: true, start: start, end: pos})
		case strings.IndexByte("{}[]():,;.", c) >= 0:
			if subst && c == '{' {
				braces++
			}
			if subst && c == '}' {
				if braces == 0 {
					return pos + 1
				}
				braces--
			}
			t := token{kind: tokPunct, text: string(c), start: pos, end: pos + 1}
			switch c {
			case '(':
				p := parenFlags(l.toks)
				l.parens = append(l.parens, p)
				t.loop, t.fn, t.meth = p.loop, p.fn, p.meth
			case ')':
				if n := len(l.parens); n > 0 {
					p := l.parens[n-1]
					l.parens = l.parens[:n-1]
					t.ctrl, t.loop, t.fn, t.meth = p.ctrl, p.loop, p.fn, p.meth
				}
			}
			l.add(t)
			pos++
		default:
			_, size := utf8.DecodeRuneInString(src[pos:])
			l.add(token{kind: tokOperator, text: src[pos : pos+size], start: pos, end: pos + size})
			pos += size
		}
	}
	if subst {
		l.unterminated = true
	}
	return pos
}

// maxRegexLength bounds the scan for a regular expression's close, so a long
// line of slashes is not rescanned from each one.
const maxRegexLength = 1000

// scanRegex reads the regular expression literal opening at source[pos] and
// returns the position after its flags. A literal that does not close on its
// line is not one.
func scanRegex(source string, pos int) (int, bool) {
	inClass := false
	for i := pos + 1; i < len(source) && i < pos+maxRegexLength; i++ {
		switch source[i] {
		case '\n':
			return 0, false
		case '\\':
			i++
		case '[':
			inClass = true
		case ']':
			inClass = false
		case '/':
			if inClass {
				continue
			}
			i++
			for i < len(source) && isIdentByte(source[i]) {
				i++
			}
			return i, true
		}
	}
	return 0, false
}

func isIdentByte(c byte) bool {
	return c == '_' || c == '$' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= 0x80
}

// lexString reads the string literal opening at source[pos] and returns its
// decoded value, whether the value is fully static (a template literal with a
// ${...} substitution is not), and the position after the closing quote. The
// tokens of a substitution are appended to the token stream.
func (l *lexer) lexString(pos int) (string, bool, int) {
	source := l.src
	quote := source[pos]
	var out strings.Builder
	static := true
	i := pos + 1
	for i < len(source) {
		c := source[i]
		switch {
		case c == quote:
			return out.String(), static, i + 1
		case c == '\\':
			i = decodeEscape(source, i, &out)
		case quote == '`' && c == '$' && i+1 < len(source) && source[i+1] == '{':
			static = false
			if l.depth >= maxTemplateDepth {
				l.tooDeep = true
				return out.String(), false, len(source)
			}
			l.depth++
			i = l.run(i+2, true)
			l.depth--
		default:
			out.WriteByte(c)
			i++
		}
	}
	l.unterminated = true
	return out.String(), false, len(source)
}

// decodeEscape appends the character the JavaScript escape at source[i] (a
// backslash) stands for and returns the position after it.
func decodeEscape(source string, i int, out *strings.Builder) int {
	if i+1 >= len(source) {
		return i + 1
	}
	c := source[i+1]
	i += 2
	switch c {
	case 'n':
		out.WriteByte('\n')
	case 't':
		out.WriteByte('\t')
	case 'r':
		out.WriteByte('\r')
	case 'b':
		out.WriteByte('\b')
	case 'f':
		out.WriteByte('\f')
	case 'v':
		out.WriteByte('\v')
	case '0':
		out.WriteByte(0)
	case '\n':
	case '\r':
		if i < len(source) && source[i] == '\n' {
			i++
		}
	case 'x':
		if i+2 <= len(source) {
			if n, err := strconv.ParseUint(source[i:i+2], 16, 8); err == nil {
				out.WriteRune(rune(n))
				return i + 2
			}
		}
		out.WriteByte('x')
	case 'u':
		return decodeUnicodeEscape(source, i, out)
	default:
		out.WriteByte(c)
	}
	return i
}

func decodeUnicodeEscape(source string, i int, out *strings.Builder) int {
	if i < len(source) && source[i] == '{' {
		// At most six hex digits and the brace: a missing close must not
		// rescan the rest of the program.
		if end := strings.IndexByte(source[i:min(len(source), i+9)], '}'); end > 0 {
			if n, err := strconv.ParseUint(source[i+1:i+end], 16, 32); err == nil {
				out.WriteRune(rune(n))
				return i + end + 1
			}
		}
		out.WriteByte('u')
		return i
	}
	r, next, ok := hex4(source, i)
	if !ok {
		out.WriteByte('u')
		return i
	}
	if r >= 0xD800 && r < 0xDC00 && strings.HasPrefix(source[next:], `\u`) {
		if low, after, ok := hex4(source, next+2); ok && low >= 0xDC00 && low < 0xE000 {
			out.WriteRune(0x10000 + (r-0xD800)<<10 + (low - 0xDC00))
			return after
		}
	}
	out.WriteRune(r)
	return next
}

func hex4(source string, i int) (rune, int, bool) {
	if i+4 > len(source) {
		return 0, i, false
	}
	n, err := strconv.ParseUint(source[i:i+4], 16, 32)
	if err != nil {
		return 0, i, false
	}
	return rune(n), i + 4, true
}
