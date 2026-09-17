package adapters

import "testing"

func FuzzDecode(f *testing.F) {
	for _, s := range []string{
		`{"type":"function_call","call_id":"c","name":"n","arguments":"{}"}`,
		`{"type":"response.function_call_arguments.delta","item_id":"i","delta":"{"}`,
		`{"role":"assistant","content":[{"type":"tool_use","id":"t","name":"Bash","input":{"command":"ls"}}]}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{"}}`,
		`{"candidates":[{"content":{"parts":[{"functionCall":{"name":"f","args":{}}}]}}]}`,
		`{"id":"x","tool":{"name":"t"},"input":{"kind":"text","text":"ls"}}`,
		`{"observation":{"kind":"exit_status","exit_code":1}}`,
		`[]`, `null`, `{"type":null}`, `{"content":"not an array"}`,
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, line string) {
		for _, a := range []Adapter{Canonical{}, OpenAI{}, Anthropic{}, Gemini{}} {
			recs, err := a.Decode([]byte(line))
			if err != nil {
				continue
			}
			for _, r := range recs {
				n := 0
				for _, set := range []bool{r.Call != nil, r.Delta != nil, r.Result != nil, r.Lifecycle != nil} {
					if set {
						n++
					}
				}
				if n != 1 {
					t.Fatalf("%s: record with %d variants set", a.Name(), n)
				}
				if r.Call != nil && r.Call.Tool.Name == "" {
					t.Fatalf("%s: call without tool name", a.Name())
				}
			}
		}
	})
}
