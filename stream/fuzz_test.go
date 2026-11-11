package stream

import (
	"context"
	"strings"
	"testing"

	"github.com/baum-ai/agentcmd"
	"github.com/baum-ai/agentcmd/adapters"
	"github.com/baum-ai/agentcmd/model"
	nstd "github.com/baum-ai/agentcmd/native/standard"
	rstd "github.com/baum-ai/agentcmd/rules/standard"
)

func FuzzProcess(f *testing.F) {
	f.Add("{\"type\":\"response.output_item.added\",\"item\":{\"type\":\"function_call\",\"id\":\"a\",\"name\":\"Bash\"}}\n{\"type\":\"response.function_call_arguments.done\",\"item_id\":\"a\",\"arguments\":\"{\\\"command\\\":\\\"ls\\\"}\"}\n{\"type\":\"function_call_output\",\"call_id\":\"a\",\"output\":\"x\"}\n")
	f.Add("garbage\n\n{\"type\":\"function_call_output\",\"call_id\":\"zz\",\"output\":\"orphan\"}\n")
	e, _ := agentcmd.New(agentcmd.Config{Rules: rstd.Rules(), Native: nstd.Bindings(), Limits: agentcmd.Limits{SyntaxNodes: 2000, Actions: 200}})
	f.Fuzz(func(t *testing.T, input string) {
		p, _ := New(e, Options{Adapter: adapters.OpenAI{}, ToolNamespace: "harness", FrameBytes: 4096, BufferBytes: 8192, ActiveCalls: 4, Window: 8})
		var last int
		err := p.Process(context.Background(), strings.NewReader(input), func(ev model.Event) error {
			if ev.Sequence < last {
				t.Fatal("events out of order")
			}
			last = ev.Sequence
			if ev.Kind == model.EventAnalysis {
				if ev.Analysis == nil || ev.Analysis.Validate() != nil {
					t.Fatal("invalid analysis event")
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	})
}
