package agentcmd

import (
	"github.com/lastpersonlabs/agentcmd/native"
	nstd "github.com/lastpersonlabs/agentcmd/native/standard"
	"github.com/lastpersonlabs/agentcmd/rules"
	rstd "github.com/lastpersonlabs/agentcmd/rules/standard"
)

func stdRules() []rules.Rule        { return rstd.Rules() }
func stdBindings() []native.Binding { return nstd.Bindings() }
