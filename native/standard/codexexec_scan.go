package standard

import (
	"path"
	"strconv"
	"strings"
	"unicode/utf8"
)

// This file holds a small dependency-free JavaScript scanner for Codex's
// code-mode tool `exec`, whose input is a JavaScript program that calls
// other tools: `tools.exec_command({cmd: "...", workdir: "..."})`. The scan
// is lexical (no parse tree): it looks for object literals that are the
// direct first argument of a known call. Nothing is evaluated.

// maxCodexExecCalls bounds the commands taken from one program.
const maxCodexExecCalls = 256

// codexExecCall is one call found in a code-mode program.
type codexExecCall struct {
	// Callee is "exec_command" or "shell".
	Callee string
	// Command is the shell text; CommandStatic is false when it is built at
	// run time, or when a spread or computed key could set it.
	Command       string
	CommandStatic bool
	// Workdir is the literal directory; HasWorkdir says the object names
	// one, WorkdirStatic that its value is a plain string literal.
	Workdir                   string
	HasWorkdir, WorkdirStatic bool
	// Start and End delimit the argument object in the program source;
	// CmdStart and CmdEnd the command's value (zero when there is none).
	Start, End       int
	CmdStart, CmdEnd int
}

// codexApplyPatch is an `apply_patch("...")` call with a static patch.
type codexApplyPatch struct {
	Patch      string
	Start, End int
}

// stepsPerToken bounds the work scanning a program does: the tokens of every
// object scanned, together, may not exceed this multiple of the program's
// tokens, so adversarial nesting stays linear.
const stepsPerToken = 16

// scanCodexExec returns the exec_command/shell calls and static apply_patch
// calls of a code-mode program in source order. truncated is set when
// maxCodexExecCalls was reached, unbalanced when the program's brackets do
// not pair up (it is not valid JavaScript).
func scanCodexExec(source string) (calls []codexExecCall, patches []codexApplyPatch, truncated, unbalanced bool) {
	tokens := tokenize(source)
	unbalanced = !balanced(tokens)
	budget := stepsPerToken * len(tokens)
	for i := 3; i < len(tokens) && budget > 0; i++ {
		if !tokens[i-1].isPunct("(") || tokens[i-2].kind != tokIdent {
			continue
		}
		callee := tokens[i-2].value
		switch callee {
		case "shell":
			// Only the tools.shell form: a bare `shell(` is some local function.
			if i < 4 || !tokens[i-3].isPunct(".") || tokens[i-4].kind != tokIdent || tokens[i-4].value != "tools" {
				continue
			}
		case "exec_command":
		case "apply_patch":
			if tokens[i].kind == tokString && tokens[i].static && i+1 < len(tokens) && (tokens[i+1].isPunct(")") || tokens[i+1].isPunct(",")) {
				patches = append(patches, codexApplyPatch{Patch: tokens[i].value, Start: tokens[i].start, End: tokens[i].end})
			}
			continue
		default:
			continue
		}
		if !tokens[i].isPunct("{") {
			continue
		}
		if len(calls) >= maxCodexExecCalls {
			return calls, patches, true, unbalanced
		}
		keys := "cmd"
		if callee == "shell" {
			keys = "command"
		}
		call := codexExecCall{Callee: callee, Start: tokens[i].start, End: tokens[i].end}
		found, opaque := false, false
		depth := 0
	object:
		for j := i + 1; j < len(tokens); j++ {
			if budget--; budget <= 0 {
				opaque = true
				break
			}
			t := tokens[j]
			atProperty := depth == 0 && (tokens[j-1].isPunct("{") || tokens[j-1].isPunct(","))
			call.End = t.end
			switch {
			case t.kind == tokPunct && (t.text == "{" || t.text == "[" || t.text == "("):
				if atProperty && t.text == "[" {
					opaque = true // a computed key
				}
				depth++
			case t.kind == tokPunct && (t.text == "}" || t.text == "]" || t.text == ")"):
				if depth == 0 {
					break object
				}
				depth--
			case atProperty && t.isPunct("."):
				opaque = true // a spread
			case atProperty && t.isKey() && j+2 < len(tokens) && tokens[j+1].isPunct(":"):
				switch t.value {
				case keys:
					if !found {
						call.Command, call.CommandStatic = commandValue(tokens, j+2, callee == "shell")
						call.CmdStart, call.CmdEnd = tokens[j+2].start, tokens[j+2].end
						found = true
					}
				case "workdir":
					call.HasWorkdir = true
					value := tokens[j+2]
					call.WorkdirStatic = value.kind == tokString && value.static && value.value != "" && endsValue(tokens, j+3)
					if call.WorkdirStatic {
						call.Workdir = value.value
					}
				}
			case atProperty && t.kind == tokIdent && j+1 < len(tokens) && (tokens[j+1].isPunct(",") || tokens[j+1].isPunct("}")):
				// Shorthand: the value is a variable.
				switch t.value {
				case keys:
					if !found {
						call.CommandStatic, found = false, true
					}
				case "workdir":
					call.HasWorkdir, call.WorkdirStatic = true, false
				}
			}
		}
		if opaque {
			call.CommandStatic = false
		}
		if found || opaque {
			calls = append(calls, call)
		}
	}
	return calls, patches, false, unbalanced
}

// balanced reports whether brackets pair up across the token stream.
func balanced(tokens []token) bool {
	var stack []string
	for _, t := range tokens {
		if t.kind != tokPunct {
			continue
		}
		switch t.text {
		case "(", "[", "{":
			stack = append(stack, t.text)
		case ")", "]", "}":
			want := map[string]string{")": "(", "]": "[", "}": "{"}[t.text]
			if len(stack) == 0 || stack[len(stack)-1] != want {
				return false
			}
			stack = stack[:len(stack)-1]
		}
	}
	return len(stack) == 0
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

// tokenize splits JavaScript source into identifiers, string literals,
// regular expressions, punctuation and operators. Whitespace and comments are
// dropped. A leading sentinel punctuation token lets a reader look one token
// back without a bounds check. It is approximate (no automatic semicolons,
// no template nesting beyond ${} skipping) and made to keep quotes inside
// strings, comments and regular expressions from desynchronising the scan.
func tokenize(source string) []token {
	tokens := []token{{kind: tokPunct, text: ";"}}
	for pos := 0; pos < len(source); {
		c := source[pos]
		before, startPos := len(tokens), pos
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			pos++
		case c == '/' && pos+1 < len(source) && source[pos+1] == '/':
			for pos < len(source) && source[pos] != '\n' {
				pos++
			}
		case c == '/' && pos+1 < len(source) && source[pos+1] == '*':
			end := strings.Index(source[pos+2:], "*/")
			if end < 0 {
				return tokens
			}
			pos += 2 + end + 2
		case c == '"' || c == '\'' || c == '`':
			value, static, next := scanString(source, pos)
			tokens = append(tokens, token{kind: tokString, value: value, static: static})
			pos = next
		case c == '/' && regexAllowed(tokens[len(tokens)-1]):
			if next, ok := scanRegex(source, pos); ok {
				tokens = append(tokens, token{kind: tokRegex})
				pos = next
				break
			}
			tokens = append(tokens, token{kind: tokOperator, text: "/"})
			pos++
		case isIdentByte(c):
			start := pos
			for pos < len(source) && isIdentByte(source[pos]) {
				pos++
			}
			word := source[start:pos]
			tokens = append(tokens, token{kind: tokIdent, text: word, value: word, static: true})
		case strings.IndexByte("{}[]():,;.", c) >= 0:
			tokens = append(tokens, token{kind: tokPunct, text: string(c)})
			pos++
		default:
			_, size := utf8.DecodeRuneInString(source[pos:])
			tokens = append(tokens, token{kind: tokOperator, text: source[pos : pos+size]})
			pos += size
		}
		if len(tokens) > before {
			tokens[len(tokens)-1].start, tokens[len(tokens)-1].end = startPos, pos
		}
	}
	return tokens
}

// regexAllowed reports whether a slash after prev starts a regular expression.
func regexAllowed(prev token) bool {
	switch prev.kind {
	case tokIdent:
		return regexKeywords[prev.value]
	case tokString, tokRegex:
		return false
	case tokPunct:
		return prev.text != ")" && prev.text != "]"
	}
	return true
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

// scanString reads the string literal opening at source[pos] and returns its
// decoded value, whether the value is fully static (a template literal with a
// ${...} substitution is not), and the position after the closing quote.
func scanString(source string, pos int) (string, bool, int) {
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
			i = skipSubstitution(source, i+2)
		default:
			out.WriteByte(c)
			i++
		}
	}
	return out.String(), false, len(source)
}

// skipSubstitution moves past a template literal's ${...} body, which may
// itself hold braces and strings.
func skipSubstitution(source string, i int) int {
	depth := 1
	for i < len(source) && depth > 0 {
		switch source[i] {
		case '{':
			depth++
		case '}':
			depth--
		case '"', '\'', '`':
			_, _, next := scanString(source, i)
			i = next
			continue
		}
		i++
	}
	return i
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
