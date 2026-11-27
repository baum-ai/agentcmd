package languages_test

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/baum-ai/agentcmd"
	"github.com/baum-ai/agentcmd/languages"
	"github.com/baum-ai/agentcmd/model"
	rstd "github.com/baum-ai/agentcmd/rules/standard"
)

func engine(t *testing.T) *agentcmd.Engine {
	t.Helper()
	e, err := agentcmd.New(agentcmd.Config{Rules: rstd.Rules(), Frontend: languages.Frontend(), Languages: agentcmd.Languages{Python: true, JavaScript: true}})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func program(lang, src string) model.Invocation {
	return model.Invocation{ID: "p", Tool: model.ToolIdentity{Name: lang}, Input: model.Input{Kind: model.InputSource, Language: lang, Source: src},
		Context: model.ExecutionContext{WorkingDir: model.KnownFact("/w", "reported"), Namespace: model.Namespace{Kind: model.NamespaceHost}}}
}

// TestParseTimeLimitIsReported: a parse stopped by its time budget makes
// Analyze return ErrTimeLimit (with ErrPartial), so a caller can tell an
// analysis that depends on timing from one that does not.
func TestParseTimeLimitIsReported(t *testing.T) {
	defer languages.SetParseBudget(time.Nanosecond)()
	e := engine(t)
	for _, inv := range []model.Invocation{
		program("javascript", "require('fs').rmSync('x')"),
		program("python", "import os\nos.remove('x')\n"),
	} {
		a, err := e.Analyze(context.Background(), inv)
		if !errors.Is(err, agentcmd.ErrTimeLimit) || !errors.Is(err, agentcmd.ErrPartial) {
			t.Fatalf("%s: err %v, want ErrTimeLimit and ErrPartial", inv.Input.Language, err)
		}
		if !a.Partial {
			t.Fatalf("%s: the analysis is not partial", inv.Input.Language)
		}
		for _, act := range a.Actions {
			// Only the interpreter itself: nothing from the program's
			// partial tree.
			if act.Kind != model.ProcessInterpreter {
				t.Fatalf("%s: action %s from a parse stopped at its time budget", inv.Input.Language, act.Kind)
			}
		}
	}
}

// TestJavaScriptAnalysisDeterministicUnderLoad: the input that failed
// FuzzAnalyzeJavaScript on 2026-11-10 (ENG-1910), analyzed concurrently
// many times, gives one analysis every time it returns no error.
func TestJavaScriptAnalysisDeterministicUnderLoad(t *testing.T) {
	e := engine(t)
	inv := program("javascript", "0{0()0\x05\xff")
	want, err := e.Analyze(context.Background(), inv)
	if err != nil {
		t.Fatal(err)
	}
	n := 64
	if testing.Short() {
		n = 16
	}
	var wg sync.WaitGroup
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			a, err := e.Analyze(context.Background(), inv)
			switch {
			case errors.Is(err, agentcmd.ErrTimeLimit):
				// Load-dependent, and reported as such.
			case err != nil:
				t.Error(err)
			case !reflect.DeepEqual(a, want):
				t.Error("analysis is not deterministic")
			}
		}()
	}
	wg.Wait()
}
