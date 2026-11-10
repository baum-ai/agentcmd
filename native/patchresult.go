package native

import (
	"github.com/baum-ai/agentcmd/model"
	"github.com/baum-ai/agentcmd/native/patch"
)

// PatchResult turns patch text into native.patch plus per-file operations.
// Inserted source is content: it yields no execution actions.
func PatchResult(inv *model.Invocation, text string) (Result, error) {
	p, err := patch.Parse(text)
	if err != nil {
		return Result{Detail: "patch", Diagnostics: []model.Diagnostic{{Code: model.DiagUnsupported, Severity: model.SeverityWarning, Message: err.Error()}},
			Actions: []model.Action{{Kind: model.NativePatch, Evidence: model.Evidence{Basis: model.BasisToolContract, Assumptions: []string{"patch could not be parsed; files unknown"}}, Native: &model.NativeDetail{Operation: "apply", Format: "unknown"}}}}, nil
	}
	res := Result{Detail: "patch (" + string(p.Format) + ")"}
	res.Actions = append(res.Actions, model.Action{Kind: model.NativePatch, Evidence: model.Evidence{Basis: model.BasisToolContract, Assumptions: []string{"file list is as declared by the patch; application outcome is separate evidence"}},
		Native: &model.NativeDetail{Operation: "apply", Format: string(p.Format), Files: p.Files}})
	for _, f := range p.Files {
		target := Resource(inv, f.Path, "target")
		ev := model.Evidence{Basis: model.BasisToolContract}
		switch f.Operation {
		case "add":
			res.Actions = append(res.Actions, model.Action{Kind: model.FSWrite, Write: &model.WriteDetail{Mode: model.WriteCreateExclusive}, Evidence: ev, Resources: []model.Resource{target}})
		case "update":
			res.Actions = append(res.Actions, model.Action{Kind: model.FSWrite, Write: &model.WriteDetail{Mode: model.WriteInPlace}, Evidence: ev, Resources: []model.Resource{target}})
		case "delete":
			res.Actions = append(res.Actions, model.Action{Kind: model.FSDelete, Delete: &model.DeleteDetail{}, Evidence: ev, Resources: []model.Resource{target}})
		case "move":
			target.Role = "source"
			res.Actions = append(res.Actions, model.Action{Kind: model.FSMove, Evidence: ev, Resources: []model.Resource{target, Resource(inv, f.MoveTo, "destination")}})
			if f.Hunks > 0 {
				res.Actions = append(res.Actions, model.Action{Kind: model.FSWrite, Write: &model.WriteDetail{Mode: model.WriteInPlace}, Evidence: ev, Resources: []model.Resource{Resource(inv, f.MoveTo, "target")}})
			}
		}
	}
	for _, b := range p.Binary {
		res.Diagnostics = append(res.Diagnostics, model.Diagnostic{Code: model.DiagUnsupported, Severity: model.SeverityInfo, Message: "binary patch content for " + b + " is not inspected"})
	}
	return res, nil
}
