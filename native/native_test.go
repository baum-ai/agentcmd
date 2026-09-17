package native

import (
	"testing"

	"github.com/lastpersonlabs/agentcmd/model"
)

func TestRegistryLookupByFingerprint(t *testing.T) {
	bind := func(*model.Invocation) (Result, error) { return Result{}, nil }
	generic := Binding{ID: "g", Version: "1", Namespace: "h", Tool: "Read", Bind: bind}
	pinned := Binding{ID: "p", Version: "1", Namespace: "h", Tool: "Read", SchemaFingerprint: "abc", Bind: bind}
	if _, err := NewRegistry(generic, generic); err == nil {
		t.Fatal("duplicate must fail")
	}
	r, err := NewRegistry(generic, pinned)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := r.Lookup(model.ToolIdentity{Namespace: "h", Name: "Read", SchemaFingerprint: "abc"}); b.ID != "p" {
		t.Fatal("pinned binding should win")
	}
	if b, _ := r.Lookup(model.ToolIdentity{Namespace: "h", Name: "Read", SchemaFingerprint: "zzz"}); b.ID != "g" {
		t.Fatal("fallback to generic")
	}
	if _, ok := r.Lookup(model.ToolIdentity{Namespace: "other", Name: "Read"}); ok {
		t.Fatal("namespace must match")
	}
}

func TestResource(t *testing.T) {
	inv := &model.Invocation{Context: model.ExecutionContext{WorkingDir: model.KnownFact("/w", "reported"), Namespace: model.Namespace{Kind: model.NamespaceHost}}}
	if r := Resource(inv, "a/b", "target"); r.Resolved != "/w/a/b" {
		t.Fatalf("%+v", r)
	}
	unknown := &model.Invocation{}
	if r := Resource(unknown, "a", "target"); r.Resolved != "" || r.Namespace.Kind != model.NamespaceUnknown {
		t.Fatalf("%+v", r)
	}
	if r := Resource(unknown, "", "target"); r.Knowledge != model.KnowledgeUnknown {
		t.Fatalf("%+v", r)
	}
}
