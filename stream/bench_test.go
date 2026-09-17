package stream

import (
	"context"
	"fmt"
	"io"
	"runtime"
	"strings"
	"testing"

	"github.com/lastpersonlabs/agentcmd"
	"github.com/lastpersonlabs/agentcmd/adapters"
	"github.com/lastpersonlabs/agentcmd/model"
	nstd "github.com/lastpersonlabs/agentcmd/native/standard"
	rstd "github.com/lastpersonlabs/agentcmd/rules/standard"
)

// generator streams synthetic canonical records without materializing the
// corpus, so benchmarks can cover 100 MiB or 1 GiB.
type generator struct {
	remaining int
	i         int
	buf       []byte
}

var templates = []string{
	`{"id":"c%d","session":"s%d","call_id":"k%d","tool":{"namespace":"harness","name":"bash"},"input":{"kind":"text","text":"cd service && go test ./... 2>&1 | tee out%d.log"},"context":{"working_dir":{"known":true,"value":"/w"},"shell":{"known":true,"value":"bash"},"namespace":{"kind":"host"}}}`,
	`{"id":"c%d","session":"s%d","call_id":"k%d","tool":{"namespace":"harness","name":"Read"},"input":{"kind":"json","json":{"file_path":"/w/src/file%d.go"}},"context":{"working_dir":{"known":true,"value":"/w"},"shell":{"known":false},"namespace":{"kind":"host"}}}`,
	`{"id":"c%d","session":"s%d","call_id":"k%d","tool":{"namespace":"harness","name":"bash"},"input":{"kind":"text","text":"grep -rn TODO src --include='*.go' && rm -rf build%d && mkdir -p build/out && cp -r assets build/out"},"context":{"working_dir":{"known":true,"value":"/w"},"shell":{"known":true,"value":"bash"},"namespace":{"kind":"host"}}}`,
	`{"session":"s%d","call_id":"k%d","observation":{"kind":"exit_status","exit_code":%d,"collector":"bench%d"}}`,
}

func (g *generator) Read(p []byte) (int, error) {
	for len(g.buf) == 0 {
		if g.remaining <= 0 {
			return 0, io.EOF
		}
		i := g.i
		g.i++
		tmpl := templates[i%len(templates)]
		var line string
		if i%len(templates) == 3 {
			line = fmt.Sprintf(tmpl, (i-1)/4, i-1, i%2, i)
		} else {
			line = fmt.Sprintf(tmpl, i, i/4, i, i)
		}
		line += "\n"
		g.remaining -= len(line)
		g.buf = []byte(line)
	}
	n := copy(p, g.buf)
	g.buf = g.buf[n:]
	return n, nil
}

func benchStream(b *testing.B, bytes int) {
	e, _ := agentcmd.New(agentcmd.Config{Rules: rstd.Rules(), Native: nstd.Bindings()})
	p, _ := New(e, Options{Adapter: adapters.Canonical{}})
	b.SetBytes(int64(bytes))
	b.ReportAllocs()
	for b.Loop() {
		n := 0
		if err := p.Process(context.Background(), &generator{remaining: bytes}, func(ev model.Event) error { n++; return nil }); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkStream1MiB(b *testing.B)   { benchStream(b, 1<<20) }
func BenchmarkStream100MiB(b *testing.B) { benchStream(b, 100<<20) }

// TestRetainedMemoryDoesNotGrowWithStreamLength processes two streams that
// differ tenfold in length and checks the live heap after each is similar.
func TestRetainedMemoryDoesNotGrowWithStreamLength(t *testing.T) {
	if testing.Short() {
		t.Skip("memory retention test skipped in -short mode")
	}
	e, _ := agentcmd.New(agentcmd.Config{Rules: rstd.Rules(), Native: nstd.Bindings()})
	p, _ := New(e, Options{Adapter: adapters.Canonical{}})
	measure := func(bytes int) uint64 {
		if err := p.Process(context.Background(), &generator{remaining: bytes}, func(model.Event) error { return nil }); err != nil {
			t.Fatal(err)
		}
		runtime.GC()
		var ms runtime.MemStats
		runtime.ReadMemStats(&ms)
		return ms.HeapAlloc
	}
	small := measure(2 << 20)
	large := measure(20 << 20)
	if large > small*2+(4<<20) {
		t.Fatalf("retained heap grew with stream length: %d -> %d bytes", small, large)
	}
	_ = strings.TrimSpace
}
