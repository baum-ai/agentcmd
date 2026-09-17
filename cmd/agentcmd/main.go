// Command agentcmd is the reference CLI. It analyzes a shell command from a
// file or stdin, or a stream of canonical invocation records, and prints
// deterministic text or JSON. It never executes input and by default does
// not print raw source.
//
//	agentcmd analyze [-cwd DIR] [-shell DIALECT] [-namespace KIND[:ID]] [-snapshot-root DIR [-snapshot-mount PATH] [-snapshot ID]] [-format text|json] [-structure] [-show-source] [FILE|-]
//	agentcmd records [-adapter NAME] [-tool-namespace NS] [-format text|jsonl] [-cwd DIR] [-shell DIALECT] [-structure] [-show-source] [FILE|-]
//	agentcmd version
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	toolparser "github.com/lastpersonlabs/toolparser"
	"github.com/lastpersonlabs/toolparser/adapters"
	"github.com/lastpersonlabs/toolparser/model"
	nstd "github.com/lastpersonlabs/toolparser/native/standard"
	"github.com/lastpersonlabs/toolparser/render"
	rstd "github.com/lastpersonlabs/toolparser/rules/standard"
	"github.com/lastpersonlabs/toolparser/snapshot"
	"github.com/lastpersonlabs/toolparser/stream"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

const usage = `usage:
  agentcmd analyze [-cwd DIR] [-shell DIALECT] [-namespace KIND[:ID]] [-snapshot-root DIR [-snapshot-mount PATH] [-snapshot ID]] [-format text|json] [-structure] [-show-source] [FILE|-]
  agentcmd records [-adapter canonical|openai|anthropic|gemini] [-tool-namespace NS] [-format text|jsonl] [-cwd DIR] [-shell DIALECT] [-structure] [-show-source] [FILE|-]
  agentcmd version
`

// run executes the CLI and returns the exit status: 0 on success, 1 for a
// usage or I/O error, 2 when any analysis was partial or a record failed.
func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 1
	}
	switch args[0] {
	case "analyze":
		return analyze(args[1:], stdin, stdout, stderr)
	case "records":
		return records(args[1:], stdin, stdout, stderr)
	case "version":
		fmt.Fprintf(stdout, "agentcmd %s schema %s\n", toolparser.Version, model.SchemaVersion)
		return 0
	case "-h", "--help", "help":
		fmt.Fprint(stdout, usage)
		return 0
	}
	fmt.Fprintf(stderr, "unknown command %q\n%s", args[0], usage)
	return 1
}

func newEngine() (*toolparser.Engine, error) {
	return toolparser.New(toolparser.Config{Rules: rstd.Rules(), Native: nstd.Bindings(), Languages: toolparser.Languages{Python: true, JavaScript: true}})
}

func openInput(name string, stdin io.Reader) (io.ReadCloser, error) {
	if name == "" || name == "-" {
		return io.NopCloser(stdin), nil
	}
	return os.Open(name)
}

func analyze(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("analyze", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cwd := fs.String("cwd", "", "reported working directory (unknown when empty)")
	shell := fs.String("shell", "bash", "shell dialect: bash, sh, zsh, mksh")
	ns := fs.String("namespace", "host", "execution namespace KIND[:ID]: host, container:ID, remote:ID, unknown")
	format := fs.String("format", "text", "output format: text or json")
	structure := fs.Bool("structure", false, "print the execution tree")
	showSource := fs.Bool("show-source", false, "include retained source text (may reveal secrets)")
	snapRoot := fs.String("snapshot-root", "", "directory whose contents stand for the working tree at -snapshot (read-only, root-confined)")
	snapMount := fs.String("snapshot-mount", "", "path in the command's namespace that -snapshot-root corresponds to (default: -cwd)")
	snapID := fs.String("snapshot", "", "snapshot identity the command ran against; content from -snapshot-root at another identity is labeled approximate")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() > 1 {
		fmt.Fprintln(stderr, "analyze: at most one input file")
		return 1
	}
	in, err := openInput(fs.Arg(0), stdin)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	defer in.Close()
	cfg := toolparser.Config{Rules: rstd.Rules(), Native: nstd.Bindings(), Languages: toolparser.Languages{Python: true, JavaScript: true}}
	if *snapRoot != "" {
		mount := *snapMount
		if mount == "" {
			mount = *cwd
		}
		if mount == "" {
			fmt.Fprintln(stderr, "analyze: -snapshot-root needs -snapshot-mount or -cwd")
			return 1
		}
		cfg.Resolver = &snapshot.Dir{Root: *snapRoot, Mount: mount, Snapshot: *snapID}
	}
	e, err := toolparser.New(cfg)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	src, err := io.ReadAll(io.LimitReader(in, int64(toolparser.DefaultLimits().InputFrameBytes)+1))
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	ec := model.ExecutionContext{Shell: model.KnownFact(*shell, "configured"), Namespace: parseNamespace(*ns), Snapshot: *snapID}
	if *cwd != "" {
		ec.WorkingDir = model.KnownFact(*cwd, "configured")
	}
	a, err := e.AnalyzeShell(context.Background(), src, ec)
	if err != nil && !errors.Is(err, toolparser.ErrPartial) {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if err := emit(stdout, &a, *format == "json", render.TextOptions{ShowSource: *showSource, Structure: *structure}); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if a.Partial {
		return 2
	}
	return 0
}

func emit(w io.Writer, a *model.Analysis, asJSON bool, opts render.TextOptions) error {
	if asJSON {
		return render.JSON(w, a, true)
	}
	return render.Text(w, a, opts)
}

func parseNamespace(s string) model.Namespace {
	kind, id, _ := strings.Cut(s, ":")
	return model.Namespace{Kind: model.NamespaceKind(kind), ID: id}
}

func records(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("records", flag.ContinueOnError)
	fs.SetOutput(stderr)
	format := fs.String("format", "text", "output format: text or jsonl")
	adapter := fs.String("adapter", "canonical", "record format: "+strings.Join(adapters.Names(), ", "))
	toolNS := fs.String("tool-namespace", "", "harness namespace to bind provider tool calls against (e.g. harness)")
	cwd := fs.String("cwd", "", "default working directory for records without context")
	shell := fs.String("shell", "", "default shell dialect for records without context")
	structure := fs.Bool("structure", false, "print the execution tree")
	showSource := fs.Bool("show-source", false, "include retained source text (may reveal secrets)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	ad, err := adapters.ByName(*adapter)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	in, err := openInput(fs.Arg(0), stdin)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	defer in.Close()
	e, err := newEngine()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	defaults := model.ExecutionContext{Namespace: model.Namespace{Kind: model.NamespaceHost}}
	if *cwd != "" {
		defaults.WorkingDir = model.KnownFact(*cwd, "configured")
	}
	if *shell != "" {
		defaults.Shell = model.KnownFact(*shell, "configured")
	}
	p, err := stream.New(e, stream.Options{Adapter: ad, Context: defaults, ToolNamespace: *toolNS})
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	status := 0
	enc := json.NewEncoder(stdout)
	err = p.Process(context.Background(), in, func(ev model.Event) error {
		if ev.Kind != model.EventAnalysis && ev.Kind != model.EventObservation || (ev.Analysis != nil && ev.Analysis.Partial) {
			status = 2
		}
		if *format == "jsonl" || *format == "json" {
			return enc.Encode(ev)
		}
		return renderEvent(stdout, ev, render.TextOptions{ShowSource: *showSource, Structure: *structure})
	})
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return status
}

func renderEvent(w io.Writer, ev model.Event, opts render.TextOptions) error {
	switch ev.Kind {
	case model.EventAnalysis:
		fmt.Fprintf(w, "record %d: %s\n", ev.Sequence, ev.Invocation)
		if err := render.Text(w, ev.Analysis, opts); err != nil {
			return err
		}
		_, err := fmt.Fprintln(w)
		return err
	case model.EventObservation:
		detail := ""
		if ev.Observation != nil {
			detail = string(ev.Observation.Kind) + " " + ev.Observation.Detail
			if ev.Observation.ExitCode != nil {
				detail += fmt.Sprintf(" exit=%d", *ev.Observation.ExitCode)
			}
		}
		for _, l := range ev.Lifecycle {
			detail += " " + string(l.Phase)
		}
		_, err := fmt.Fprintf(w, "record %d: observation for %s:%s\n\n", ev.Sequence, ev.Invocation, detail)
		return err
	default:
		msg := ""
		if ev.Diagnostic != nil {
			msg = ev.Diagnostic.Code + ": " + ev.Diagnostic.Message
		}
		_, err := fmt.Fprintf(w, "record %d: %s %s\n\n", ev.Sequence, ev.Kind, msg)
		return err
	}
}
