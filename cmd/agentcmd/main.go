// Command agentcmd is the reference CLI. It analyzes a shell command from a
// file or stdin, or a stream of canonical invocation records, and prints
// deterministic text or JSON. It never executes input and by default does
// not print raw source.
//
//	agentcmd analyze [-cwd DIR] [-shell DIALECT] [-namespace KIND[:ID]] [-format text|json] [-structure] [-show-source] [FILE|-]
//	agentcmd records [-format text|jsonl] [-structure] [-show-source] [FILE|-]
//	agentcmd version
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	toolparser "github.com/lastpersonlabs/toolparser"
	"github.com/lastpersonlabs/toolparser/model"
	nstd "github.com/lastpersonlabs/toolparser/native/standard"
	"github.com/lastpersonlabs/toolparser/render"
	rstd "github.com/lastpersonlabs/toolparser/rules/standard"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

const usage = `usage:
  agentcmd analyze [-cwd DIR] [-shell DIALECT] [-namespace KIND[:ID]] [-format text|json] [-structure] [-show-source] [FILE|-]
  agentcmd records [-format text|jsonl] [-structure] [-show-source] [FILE|-]
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
	return toolparser.New(toolparser.Config{Rules: rstd.Rules(), Native: nstd.Bindings()})
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
	e, err := newEngine()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	src, err := io.ReadAll(io.LimitReader(in, int64(toolparser.DefaultLimits().InputFrameBytes)+1))
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	ec := model.ExecutionContext{Shell: model.KnownFact(*shell, "configured"), Namespace: parseNamespace(*ns)}
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

// recordResult is one output line in records mode.
type recordResult struct {
	Line       int             `json:"line"`
	Invocation string          `json:"invocation,omitempty"`
	Analysis   *model.Analysis `json:"analysis,omitempty"`
	Error      string          `json:"error,omitempty"`
}

func records(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("records", flag.ContinueOnError)
	fs.SetOutput(stderr)
	format := fs.String("format", "text", "output format: text or jsonl")
	structure := fs.Bool("structure", false, "print the execution tree")
	showSource := fs.Bool("show-source", false, "include retained source text (may reveal secrets)")
	if err := fs.Parse(args); err != nil {
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
	limit := toolparser.DefaultLimits().InputFrameBytes
	r := bufio.NewReaderSize(in, 64<<10)
	enc := json.NewEncoder(stdout)
	status := 0
	for line := 1; ; line++ {
		data, tooLong, err := readLine(r, limit)
		if err == io.EOF && len(data) == 0 {
			break
		}
		if err != nil && err != io.EOF {
			fmt.Fprintln(stderr, err)
			return 1
		}
		res := recordResult{Line: line}
		switch {
		case tooLong:
			res.Error = fmt.Sprintf("record exceeds %d bytes and was skipped", limit)
		case len(strings.TrimSpace(string(data))) == 0:
			continue
		default:
			var inv model.Invocation
			if uerr := json.Unmarshal(data, &inv); uerr != nil {
				res.Error = "invalid record: " + uerr.Error()
				break
			}
			res.Invocation = inv.ID
			a, aerr := e.Analyze(context.Background(), inv)
			if aerr != nil && !errors.Is(aerr, toolparser.ErrPartial) {
				res.Error = aerr.Error()
				break
			}
			res.Analysis = &a
		}
		if res.Error != "" || (res.Analysis != nil && res.Analysis.Partial) {
			status = 2
		}
		if *format == "jsonl" || *format == "json" {
			if err := enc.Encode(res); err != nil {
				fmt.Fprintln(stderr, err)
				return 1
			}
			continue
		}
		if res.Error != "" {
			fmt.Fprintf(stdout, "record %d: %s\n\n", line, res.Error)
			continue
		}
		fmt.Fprintf(stdout, "record %d:\n", line)
		if err := render.Text(stdout, res.Analysis, render.TextOptions{ShowSource: *showSource, Structure: *structure}); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		fmt.Fprintln(stdout)
	}
	return status
}

// readLine reads one line, discarding the remainder of lines longer than max
// without buffering them in full.
func readLine(r *bufio.Reader, max int) ([]byte, bool, error) {
	var line []byte
	tooLong := false
	for {
		chunk, isPrefix, err := r.ReadLine()
		if err != nil {
			return line, tooLong, err
		}
		if !tooLong {
			if len(line)+len(chunk) > max {
				tooLong = true
				line = nil
			} else {
				line = append(line, chunk...)
			}
		}
		if !isPrefix {
			return line, tooLong, nil
		}
	}
}
