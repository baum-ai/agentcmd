package recountable

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/lastpersonlabs/agentcmd"
	"github.com/lastpersonlabs/agentcmd/model"
	rstd "github.com/lastpersonlabs/agentcmd/rules/standard"
)

func TestProjectFlattensWithoutSourceText(t *testing.T) {
	e, _ := agentcmd.New(agentcmd.Config{Rules: rstd.Rules()})
	a, _ := e.AnalyzeShell(context.Background(), []byte("cd x && make test; secret_cmd --token abc"), model.ExecutionContext{WorkingDir: model.KnownFact("/w", "reported"), Namespace: model.Namespace{Kind: model.NamespaceHost}})
	rec := Project(&a)
	raw, _ := json.Marshal(rec)
	if strings.Contains(string(raw), "abc") || strings.Contains(string(raw), `"text"`) {
		t.Fatal("projection must not carry source text or raw arguments")
	}
	if rec.Analysis != a.ID || len(rec.Actions) == 0 || len(rec.Intents) != 1 || rec.Intents[0].Verified {
		t.Fatalf("%+v", rec)
	}
	found := false
	for _, ar := range rec.Actions {
		if ar.Kind == model.DevTest && ar.Guarded && ar.Reach == model.ReachConditional {
			found = true
		}
	}
	if !found {
		t.Fatalf("%+v", rec.Actions)
	}
	if rec.Sources[0].Redacted != true || rec.Sources[0].Length == 0 {
		t.Fatalf("%+v", rec.Sources)
	}
}
