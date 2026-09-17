// Package observe attaches dynamic evidence (tool results, exit statuses,
// file or process observations) to the entity of an analysis it covers.
// Observations never change an action's basis; they are added alongside.
package observe

import (
	"fmt"
	"strings"

	"github.com/lastpersonlabs/agentcmd/model"
)

// Attach adds an observation to the analysis, choosing the subject from the
// observation kind when none is given:
//
//   - exit status and tool results cover the invocation;
//   - a file change confirms the filesystem actions on the same resolved
//     resource in the same namespace, or informs the invocation when none
//     matches;
//   - a test result covers the single development.test action if there is
//     exactly one, else the invocation;
//   - a patch result covers the single native.patch action if there is one.
//
// The analysis is validated afterwards.
func Attach(a *model.Analysis, o model.Observation) error {
	if o.Basis == "" || o.Basis.Static() {
		return fmt.Errorf("observe: basis %q is not dynamic evidence", o.Basis)
	}
	if o.Namespace.Kind == "" {
		o.Namespace.Kind = model.NamespaceUnknown
	}
	if o.Relation == "" {
		o.Relation = model.RelationInforms
	}
	if o.Subject == "" {
		o.Subject = subject(a, &o)
	}
	o.ID = fmt.Sprintf("o%d", len(a.Observations)+1)
	a.Observations = append(a.Observations, o)
	return a.Validate()
}

func subject(a *model.Analysis, o *model.Observation) string {
	switch o.Kind {
	case model.ObsFileChange:
		if o.Resource == nil || o.Resource.Resolved == "" {
			return a.Invocation
		}
		var matches []string
		for _, act := range a.Actions {
			if act.Kind.Family() != "filesystem" {
				continue
			}
			for _, r := range act.Resources {
				if r.Resolved == o.Resource.Resolved && r.Namespace == o.Resource.Namespace {
					matches = append(matches, act.ID)
					break
				}
			}
		}
		if len(matches) == 0 {
			o.Detail = strings.TrimSpace("no inferred filesystem action targets this path; the change may come from another process or an unmodeled effect. " + o.Detail)
			return a.Invocation
		}
		o.Relation = model.RelationConfirms
		if len(matches) > 1 {
			o.Detail = strings.TrimSpace(fmt.Sprintf("also consistent with %s. ", strings.Join(matches[1:], ", ")) + o.Detail)
		}
		return matches[0]
	case model.ObsTestResult:
		return single(a, model.DevTest, a.Invocation)
	case model.ObsPatchResult:
		return single(a, model.NativePatch, a.Invocation)
	}
	return a.Invocation
}

func single(a *model.Analysis, kind model.ActionKind, fallback string) string {
	var found []string
	for _, act := range a.Actions {
		if act.Kind == kind {
			found = append(found, act.ID)
		}
	}
	if len(found) == 1 {
		return found[0]
	}
	return fallback
}

// ExitStatus builds an exit-status observation for the invocation's own
// process. It says nothing about the operations inside it.
func ExitStatus(code int, collector string) model.Observation {
	success := code == 0
	return model.Observation{Kind: model.ObsExitStatus, Basis: model.BasisToolReport, Collector: collector, ExitCode: &code, Success: &success,
		Detail: "exit status of the outer process; inner operations may have failed or partially completed"}
}

// FileChange builds a runtime observation of a changed path.
func FileChange(resolved string, ns model.Namespace, change, collector string) model.Observation {
	return model.Observation{Kind: model.ObsFileChange, Basis: model.BasisRuntime, Collector: collector, Namespace: ns,
		Resource: &model.Resource{Expression: resolved, Resolved: resolved, Knowledge: model.KnowledgeLiteral, Namespace: ns}, Detail: change}
}
