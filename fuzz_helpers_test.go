package agentcmd

import (
	"github.com/baum-ai/agentcmd/native"
	nstd "github.com/baum-ai/agentcmd/native/standard"
	"github.com/baum-ai/agentcmd/rules"
	rstd "github.com/baum-ai/agentcmd/rules/standard"
)

func stdRules() []rules.Rule        { return rstd.Rules() }
func stdBindings() []native.Binding { return nstd.Bindings() }
