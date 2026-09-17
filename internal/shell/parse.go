// Package shell wraps mvdan.cc/sh/v3/syntax and lowers shell syntax into the
// owned execution representation. No mvdan type escapes this package.
package shell

import (
	"bytes"
	"errors"
	"fmt"

	"mvdan.cc/sh/v3/syntax"
)

// ParserVersion is the pinned upstream parser version recorded in results.
const ParserVersion = "mvdan.cc/sh/v3@v3.14.1"

// Dialect selects the shell grammar. Only POSIX and Bash are semantically
// certified; the others parse but their semantics are reported unsupported.
type Dialect string

const (
	DialectBash  Dialect = "bash"
	DialectPOSIX Dialect = "sh"
	DialectZsh   Dialect = "zsh"
	DialectMksh  Dialect = "mksh"
)

// Certified reports whether semantic analysis is certified for the dialect.
func (d Dialect) Certified() bool { return d == DialectBash || d == DialectPOSIX }

func (d Dialect) variant() (syntax.LangVariant, error) {
	switch d {
	case DialectBash, "":
		return syntax.LangBash, nil
	case DialectPOSIX:
		return syntax.LangPOSIX, nil
	case DialectZsh:
		return syntax.LangZsh, nil
	case DialectMksh:
		return syntax.LangMirBSDKorn, nil
	}
	return 0, fmt.Errorf("unsupported shell dialect %q", d)
}

// ParseStatus classifies the outcome of parsing.
type ParseStatus string

const (
	StatusParsed      ParseStatus = "parsed"
	StatusPartial     ParseStatus = "partial" // input ended inside a construct
	StatusInvalid     ParseStatus = "invalid"
	StatusUnsupported ParseStatus = "unsupported"
)

// SyntaxError is returned for invalid or incomplete input with a byte offset.
type SyntaxError struct {
	Offset int
	Msg    string
	Status ParseStatus
}

func (e *SyntaxError) Error() string { return fmt.Sprintf("offset %d: %s", e.Offset, e.Msg) }

// parse runs the upstream parser. It never evaluates anything. The upstream
// lexer rejects non-UTF-8 input, so such sources are reported invalid at the
// offending offset.
func parse(src []byte, d Dialect) (*syntax.File, error) {
	v, err := d.variant()
	if err != nil {
		return nil, &SyntaxError{Msg: err.Error(), Status: StatusUnsupported}
	}
	p := syntax.NewParser(syntax.Variant(v), syntax.KeepComments(false))
	f, err := p.Parse(bytes.NewReader(src), "")
	if err != nil {
		se := &SyntaxError{Msg: err.Error(), Status: StatusInvalid}
		switch e := err.(type) {
		case syntax.ParseError:
			// For incomplete input the position is the start of the
			// unclosed construct, not the end of the input.
			se.Offset, se.Msg = int(e.Pos.Offset()), e.Text
			if e.Incomplete {
				se.Status = StatusPartial
			}
		case syntax.LangError:
			se.Offset, se.Msg = int(e.Pos.Offset()), e.Error()
		}
		return nil, se
	}
	return f, nil
}

// Check parses src and reports its status. It is the qualification surface
// used before lowering exists and stays useful for cheap validation.
func Check(src []byte, d Dialect) (ParseStatus, error) {
	if _, err := parse(src, d); err != nil {
		var se *SyntaxError
		if errors.As(err, &se) {
			return se.Status, err
		}
		return StatusInvalid, err
	}
	return StatusParsed, nil
}
