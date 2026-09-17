package toolparser

import (
	"github.com/lastpersonlabs/toolparser/native"
	nstd "github.com/lastpersonlabs/toolparser/native/standard"
	"github.com/lastpersonlabs/toolparser/rules"
	rstd "github.com/lastpersonlabs/toolparser/rules/standard"
)

func stdRules() []rules.Rule        { return rstd.Rules() }
func stdBindings() []native.Binding { return nstd.Bindings() }
