package rules

import (
	"fmt"
	"strings"

	"github.com/lastpersonlabs/agentcmd/model"
)

// OptionSpec is a documented option grammar for one command.
type OptionSpec struct {
	// ShortArgs lists short options that take an argument (attached or
	// following): "n" means -n NUM and -nNUM.
	ShortArgs string
	// ShortFlags lists known short options without arguments.
	ShortFlags string
	// LongArgs lists long options taking an argument: --lines=N or --lines N.
	LongArgs []string
	// LongFlags lists known long options without arguments.
	LongFlags []string
	// NoCluster disables combined short flags (-rf) for commands that use
	// single-dash long options such as find.
	NoCluster bool
	// OperandsStopOptions makes the first operand end option parsing
	// (POSIXLY_CORRECT style, used by env, sudo, timeout).
	OperandsStopOptions bool
}

// Options is the parsed argv.
type Options struct {
	Operands []model.Value
	// Values holds arguments of options that take one, keyed without dashes.
	Values map[string][]model.Value
	// Flags holds every option seen, keyed without dashes.
	Flags map[string]bool
	// Unknown lists options not in the spec.
	Unknown []string
	// Assumptions records how ambiguity was resolved.
	Assumptions []string
	// Rest holds the arguments after the first operand when
	// OperandsStopOptions is set (wrapper commands).
	Rest []model.Value
}

// Has reports whether an option was seen.
func (o Options) Has(name string) bool { return o.Flags[name] }

// Value returns the last value of an option, if any.
func (o Options) Value(name string) (model.Value, bool) {
	vs := o.Values[name]
	if len(vs) == 0 {
		return model.Value{}, false
	}
	return vs[len(vs)-1], true
}

// ParseOptions applies spec to args. Unknown options are recorded and
// assumed to take no argument, which is stated as an assumption so operand
// extraction is qualified rather than silently wrong. Non-concrete arguments
// are treated as operands with an assumption.
func ParseOptions(args []model.Value, spec OptionSpec) Options {
	o := Options{Values: map[string][]model.Value{}, Flags: map[string]bool{}}
	longArg := map[string]bool{}
	for _, l := range spec.LongArgs {
		longArg[l] = true
	}
	longFlag := map[string]bool{}
	for _, l := range spec.LongFlags {
		longFlag[l] = true
	}
	take := func(name string, i *int) {
		o.Flags[name] = true
		if *i+1 < len(args) {
			*i++
			o.Values[name] = append(o.Values[name], args[*i])
		} else {
			o.Assumptions = append(o.Assumptions, fmt.Sprintf("option %s is missing its argument", name))
		}
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !a.Concrete() {
			o.Operands = append(o.Operands, a)
			o.Assumptions = append(o.Assumptions, fmt.Sprintf("non-literal argument %q assumed to be an operand", a.Text))
			continue
		}
		s := a.Text
		switch {
		case s == "--":
			o.Operands = append(o.Operands, args[i+1:]...)
			return o
		case strings.HasPrefix(s, "--") && len(s) > 2:
			name, val, hasEq := strings.Cut(s[2:], "=")
			o.Flags[name] = true
			switch {
			case hasEq:
				o.Values[name] = append(o.Values[name], model.Value{Knowledge: a.Knowledge, Text: val, Span: a.Span})
			case longArg[name]:
				take(name, &i)
			case !longFlag[name]:
				o.Unknown = append(o.Unknown, s)
				o.Assumptions = append(o.Assumptions, fmt.Sprintf("unknown option %s assumed to take no argument", s))
			}
		case strings.HasPrefix(s, "-") && len(s) > 1 && !spec.NoCluster:
			for j := 1; j < len(s); j++ {
				c := string(s[j])
				o.Flags[c] = true
				if strings.Contains(spec.ShortArgs, c) {
					if j+1 < len(s) {
						o.Values[c] = append(o.Values[c], model.Value{Knowledge: a.Knowledge, Text: s[j+1:], Span: a.Span})
					} else {
						take(c, &i)
					}
					break
				}
				if !strings.Contains(spec.ShortFlags, c) {
					o.Unknown = append(o.Unknown, "-"+c)
					o.Assumptions = append(o.Assumptions, fmt.Sprintf("unknown option -%s assumed to take no argument", c))
				}
			}
		case strings.HasPrefix(s, "-") && len(s) > 1:
			name := s[1:]
			o.Flags[name] = true
			switch {
			case longArg[name]:
				take(name, &i)
			case !longFlag[name]:
				o.Unknown = append(o.Unknown, s)
				o.Assumptions = append(o.Assumptions, fmt.Sprintf("unknown option %s assumed to take no argument", s))
			}
		default:
			o.Operands = append(o.Operands, a)
			if spec.OperandsStopOptions {
				o.Rest = args[i+1:]
				return o
			}
		}
	}
	return o
}
