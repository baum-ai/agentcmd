package model

// Analysis is the result of analyzing one invocation. Nodes form the
// execution representation; Actions are inferred operations attached to
// nodes; Observations are separate dynamic evidence.
type Analysis struct {
	SchemaVersion string `json:"schema_version"`
	// ID is deterministic over the invocation identity, versions, options and
	// context fingerprint. See ADR 0004.
	ID string `json:"id"`
	// Previous links a re-analysis with enriched context to its predecessor.
	Previous   string   `json:"previous,omitempty"`
	Invocation string   `json:"invocation"`
	Versions   Versions `json:"versions"`
	// Partial is set when a limit, syntax error or unsupported construct
	// prevented complete analysis. The diagnostics say why.
	Partial bool `json:"partial,omitempty"`

	Nodes        []Node        `json:"nodes"`
	Actions      []Action      `json:"actions"`
	Sources      []Source      `json:"sources"`
	Diagnostics  []Diagnostic  `json:"diagnostics,omitempty"`
	Observations []Observation `json:"observations,omitempty"`
}

// Versions records what produced the analysis.
type Versions struct {
	Analyzer        string `json:"analyzer"`
	Ruleset         string `json:"ruleset"`
	ShellParser     string `json:"shell_parser,omitempty"`
	LanguageRuntime string `json:"language_runtime,omitempty"`
}

// NodeKind classifies execution nodes.
type NodeKind string

const (
	NodeInvocation   NodeKind = "invocation"   // root
	NodeShell        NodeKind = "shell"        // a shell program
	NodeSequence     NodeKind = "sequence"     // a ; b
	NodeAnd          NodeKind = "and"          // a && b
	NodeOr           NodeKind = "or"           // a || b
	NodePipeline     NodeKind = "pipeline"     // a | b
	NodeSubshell     NodeKind = "subshell"     // ( ... )
	NodeGroup        NodeKind = "group"        // { ...; }
	NodeIf           NodeKind = "if"           // if/elif/else, case
	NodeLoop         NodeKind = "loop"         // for/while/until
	NodeFunction     NodeKind = "function"     // definition; body is latent
	NodeBackground   NodeKind = "background"   // cmd &
	NodeCommand      NodeKind = "command"      // simple command
	NodeAssignment   NodeKind = "assignment"   // bare assignment
	NodeRedirect     NodeKind = "redirect"     // one redirection, child of its command
	NodeSubstitution NodeKind = "substitution" // $(...) or <(...)
	NodeProgram      NodeKind = "program"      // embedded-language program
	NodeNative       NodeKind = "native"       // native tool operation
	NodeUnknown      NodeKind = "unknown"      // unmodelled region
)

// GuardCondition says which outcome of the guarding node enables execution.
type GuardCondition string

const (
	GuardSuccess GuardCondition = "success"
	GuardFailure GuardCondition = "failure"
)

// Guard is an execution condition on a node.
type Guard struct {
	Node string         `json:"node"`
	On   GuardCondition `json:"on"`
}

// Reachability qualifies how often a node may execute relative to its parent.
type Reachability string

const (
	ReachAlways      Reachability = "always"      // runs whenever the parent runs
	ReachConditional Reachability = "conditional" // guarded or in a branch
	ReachRepeated    Reachability = "repeated"    // inside a loop
	ReachLatent      Reachability = "latent"      // defined but not invoked here
	ReachUnreachable Reachability = "unreachable" // statically dead
	ReachUnknown     Reachability = "unknown"
)

// SyntaxStatus describes how well a source region was parsed.
type SyntaxStatus string

const (
	SyntaxParsed      SyntaxStatus = "parsed"
	SyntaxPartial     SyntaxStatus = "partial"
	SyntaxInvalid     SyntaxStatus = "invalid"
	SyntaxUnsupported SyntaxStatus = "unsupported"
	SyntaxLimit       SyntaxStatus = "limit_reached"
)

// Node is one element of the execution representation.
type Node struct {
	ID     string       `json:"id"`
	Parent string       `json:"parent,omitempty"`
	Kind   NodeKind     `json:"kind"`
	Span   *SourceSpan  `json:"span,omitempty"`
	Guard  *Guard       `json:"guard,omitempty"`
	Reach  Reachability `json:"reachability,omitempty"`
	Syntax SyntaxStatus `json:"syntax,omitempty"`
	// Detail is a short kind-specific label (loop type, redirect operator,
	// function name, unknown reason).
	Detail string `json:"detail,omitempty"`

	Command *Command    `json:"command,omitempty"`
	Program *Program    `json:"program,omitempty"`
	Native  *NativeCall `json:"native,omitempty"`
	Context NodeContext `json:"context"`
}

// NodeContext is the abstract execution context at a node.
type NodeContext struct {
	Cwd       Value     `json:"cwd"`
	Namespace Namespace `json:"namespace"`
}

// Command is a simple command with abstract argv.
type Command struct {
	Executable Value   `json:"executable"`
	Args       []Value `json:"args,omitempty"`
	// Wrappers lists wrapper executables stripped to reach this command,
	// outermost first (env, timeout, sudo, ...).
	Wrappers    []string `json:"wrappers,omitempty"`
	Assumptions []string `json:"assumptions,omitempty"`
	// Rule identifies the command rule that recognized the executable.
	Rule string `json:"rule,omitempty"`
}

// Program is an embedded-language program reached through a launcher.
type Program struct {
	Language    string `json:"language"`
	Interpreter string `json:"interpreter,omitempty"`
	// Mode is how the source reached the interpreter: inline, stdin, file,
	// module, unknown.
	Mode string `json:"mode"`
	// Source is the derived source ID holding the program text, when known.
	Source string `json:"source,omitempty"`
	// File is the script resource for mode "file".
	File *Resource `json:"file,omitempty"`
}

// NativeCall describes a structured tool operation.
type NativeCall struct {
	Namespace      string `json:"namespace,omitempty"`
	Tool           string `json:"tool"`
	Binding        string `json:"binding,omitempty"`
	BindingVersion string `json:"binding_version,omitempty"`
}

// Knowledge states how well a value or resource is known.
type Knowledge string

const (
	KnowledgeLiteral  Knowledge = "literal"  // fully literal in the source
	KnowledgeDerived  Knowledge = "derived"  // computed under stated assumptions
	KnowledgeSymbolic Knowledge = "symbolic" // contains unknown expansion
	KnowledgeGlob     Knowledge = "glob"     // a pattern; matches are not enumerated
	KnowledgeRedacted Knowledge = "redacted"
	KnowledgeUnknown  Knowledge = "unknown"
	KnowledgeUnset    Knowledge = "unset"
)

// Value is an abstract string value.
type Value struct {
	Knowledge Knowledge `json:"knowledge"`
	// Text is the literal/derived value, or a symbolic rendering such as
	// "$HOME/.bashrc" for symbolic values.
	Text         string      `json:"text,omitempty"`
	Assumptions  []string    `json:"assumptions,omitempty"`
	Alternatives []string    `json:"alternatives,omitempty"`
	Span         *SourceSpan `json:"span,omitempty"`
}

// Literal builds a literal value.
func Literal(text string) Value { return Value{Knowledge: KnowledgeLiteral, Text: text} }

// Symbolic builds a symbolic value with a rendering.
func Symbolic(text string) Value { return Value{Knowledge: KnowledgeSymbolic, Text: text} }

// Unknown is a value about which nothing is known.
func Unknown() Value { return Value{Knowledge: KnowledgeUnknown} }

// Concrete reports whether Text is the actual value (literal or derived with
// a single possibility).
func (v Value) Concrete() bool {
	return (v.Knowledge == KnowledgeLiteral || v.Knowledge == KnowledgeDerived) && len(v.Alternatives) == 0
}

// Resource is a path, URL or other operand as an expression in a namespace.
type Resource struct {
	// Role is the operand's part in the action: target, source, destination,
	// scope, input, output, stdin.
	Role       string `json:"role,omitempty"`
	Expression string `json:"expression,omitempty"`
	// ExpressionBytes carries the raw operand when it is not valid UTF-8.
	ExpressionBytes []byte `json:"expression_bytes,omitempty"`
	// Resolved is the lexical resolution against the abstract working
	// directory. It is not a symlink-canonical identity.
	Resolved string `json:"resolved,omitempty"`
	// Alternatives lists the bounded set of possible resolved values when
	// the expression joins several branches.
	Alternatives []string    `json:"alternatives,omitempty"`
	Knowledge    Knowledge   `json:"knowledge"`
	Namespace    Namespace   `json:"namespace"`
	Snapshot     string      `json:"snapshot,omitempty"`
	Span         *SourceSpan `json:"span,omitempty"`
}

// Basis is the evidence class of a statement. See ADR 0001.
type Basis string

const (
	BasisToolContract Basis = "tool_contract"
	BasisSyntax       Basis = "syntax"
	BasisBoundedValue Basis = "bounded_value"
	BasisHeuristic    Basis = "heuristic_intent"
	BasisToolReport   Basis = "tool_report"
	BasisRuntime      Basis = "runtime_observation"
)

// Static reports whether the basis is a static inference.
func (b Basis) Static() bool {
	switch b {
	case BasisToolContract, BasisSyntax, BasisBoundedValue, BasisHeuristic:
		return true
	}
	return false
}

// Evidence qualifies an action.
type Evidence struct {
	Basis       Basis        `json:"basis"`
	Rule        string       `json:"rule,omitempty"`
	RuleVersion string       `json:"rule_version,omitempty"`
	Assumptions []string     `json:"assumptions,omitempty"`
	Spans       []SourceSpan `json:"spans,omitempty"`
}

// Severity of a diagnostic.
type Severity string

const (
	SeverityInfo    Severity = "info"
	SeverityWarning Severity = "warning"
	SeverityError   Severity = "error"
)

// Diagnostic codes.
const (
	DiagSyntaxError       = "syntax_error"
	DiagUnsupported       = "unsupported"
	DiagLimitReached      = "limit_reached"
	DiagUnresolvedBinding = "unresolved_binding"
	DiagMissingSnapshot   = "missing_snapshot"
	DiagTruncated         = "truncated"
	DiagBudgetExhausted   = "budget_exhausted"
	DiagUnknownTool       = "unknown_tool"
	DiagUnknownOption     = "unknown_option"
	DiagShadowed          = "shadowed_binding"
	DiagStaleContent      = "stale_content"
)

// Diagnostic explains incompleteness or uncertainty.
type Diagnostic struct {
	Code     string      `json:"code"`
	Severity Severity    `json:"severity"`
	Message  string      `json:"message"`
	Node     string      `json:"node,omitempty"`
	Span     *SourceSpan `json:"span,omitempty"`
}

// Source is a text buffer the analyzer parsed. s0 is the invocation input.
type Source struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"` // shell, python, javascript, heredoc, json_field, argv, patch, text
	Length int    `json:"length"`
	// Text is retained only when small; otherwise the caller owns the bytes.
	Text    string      `json:"text,omitempty"`
	Derived *Derivation `json:"derived,omitempty"`
}

// Derivation maps a derived source back into its parent.
type Derivation struct {
	From string `json:"from"`
	// Exact is false when decoding (quote removal, escapes, tab stripping,
	// JSON unescaping) makes the byte map approximate.
	Exact    bool      `json:"exact"`
	Segments []Segment `json:"segments,omitempty"`
}

// Segment maps [Start,End) in the derived source to [FromStart,FromEnd) in
// the parent.
type Segment struct {
	Start     int `json:"start"`
	End       int `json:"end"`
	FromStart int `json:"from_start"`
	FromEnd   int `json:"from_end"`
}

// SourceSpan is a half-open byte range in a source.
type SourceSpan struct {
	Source string `json:"source"`
	Start  int    `json:"start"`
	End    int    `json:"end"`
}

// ObservationKind classifies dynamic evidence.
type ObservationKind string

const (
	ObsExitStatus  ObservationKind = "exit_status"
	ObsToolResult  ObservationKind = "tool_result"
	ObsPatchResult ObservationKind = "patch_result"
	ObsTestResult  ObservationKind = "test_result"
	ObsFileChange  ObservationKind = "file_change"
	ObsProcess     ObservationKind = "process"
	ObsNetwork     ObservationKind = "network"
)

// Relation of an observation to the entity it covers.
type Relation string

const (
	RelationConfirms  Relation = "confirms"
	RelationConflicts Relation = "conflicts"
	RelationInforms   Relation = "informs"
)

// Observation is dynamic evidence about one entity (invocation, node or
// action). It never changes the entity's own basis.
type Observation struct {
	ID        string          `json:"id"`
	Subject   string          `json:"subject"`
	Kind      ObservationKind `json:"kind"`
	Basis     Basis           `json:"basis"` // tool_report or runtime_observation
	Relation  Relation        `json:"relation,omitempty"`
	Collector string          `json:"collector,omitempty"`
	Namespace Namespace       `json:"namespace"`
	Time      string          `json:"time,omitempty"`
	ExitCode  *int            `json:"exit_code,omitempty"`
	Success   *bool           `json:"success,omitempty"`
	Resource  *Resource       `json:"resource,omitempty"`
	Detail    string          `json:"detail,omitempty"`
}
