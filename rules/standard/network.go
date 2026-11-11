package standard

import (
	"path"
	"strings"

	"github.com/baum-ai/agentcmd/model"
	"github.com/baum-ai/agentcmd/rules"
)

func isURL(v model.Value) bool {
	return strings.Contains(v.Text, "://") || strings.HasPrefix(v.Text, "localhost") || strings.HasPrefix(v.Text, "www.")
}

var curlRule = rules.Rule{
	ID: "std/curl", Version: "1", Executables: []string{"curl"}, Platform: "",
	Doc: "curl [options] <url>...; -o file -O -X method -d data -F form -T file -H header -u user --output --request --data --upload-file",
	Apply: func(cmd rules.Command) rules.Result {
		o := rules.ParseOptions(cmd.Args, rules.OptionSpec{ShortFlags: "fsSLkvIiOGgNnjJRZ#", ShortArgs: "oXdFTHuAebcwmxKEQrty",
			LongArgs:  []string{"output", "request", "data", "data-raw", "data-binary", "data-urlencode", "form", "upload-file", "header", "user", "user-agent", "referer", "cookie", "cookie-jar", "write-out", "max-time", "connect-timeout", "proxy", "config", "cert", "key", "cacert", "retry", "retry-delay", "url", "resolve", "output-dir", "range", "continue-at", "json"},
			LongFlags: []string{"fail", "silent", "show-error", "location", "insecure", "verbose", "head", "include", "remote-name", "remote-header-name", "compressed", "no-progress-meter", "progress-bar", "globoff", "get", "netrc", "parallel", "http1.1", "http2", "http3", "tlsv1.2", "tlsv1.3", "create-dirs", "fail-with-body", "ipv4", "ipv6", "no-buffer", "remote-time", "junk-session-cookies"}})
		res := rules.Result{Assumptions: o.Assumptions}
		urls := append(o.Operands, o.Values["url"]...)
		method := ""
		if m, ok := o.Value("X"); ok {
			method = m.Text
		} else if m, ok := o.Value("request"); ok {
			method = m.Text
		}
		hasData := len(o.Values["d"])+len(o.Values["data"])+len(o.Values["data-raw"])+len(o.Values["data-binary"])+len(o.Values["data-urlencode"])+len(o.Values["F"])+len(o.Values["form"])+len(o.Values["json"]) > 0
		uploads := append(o.Values["T"], o.Values["upload-file"]...)
		outputs := append(o.Values["o"], o.Values["output"]...)
		for i, u := range urls {
			kind := model.NetRequest
			var resources []model.Resource
			switch {
			case len(uploads) > 0:
				kind = model.NetUpload
				for _, f := range uploads {
					resources = append(resources, cmd.Resource(f, "source"))
				}
			case hasData:
				kind = model.NetUpload
				for _, d := range append(o.Values["d"], o.Values["data-binary"]...) {
					if d.Concrete() && strings.HasPrefix(d.Text, "@") {
						resources = append(resources, cmd.Resource(model.Value{Knowledge: d.Knowledge, Text: d.Text[1:], Span: d.Span}, "source"))
					}
				}
			case i < len(outputs):
				kind = model.NetDownload
				resources = append(resources, cmd.Resource(outputs[i], "destination"))
			case o.Has("O") || o.Has("remote-name"):
				kind = model.NetDownload
				name := model.Value{Knowledge: model.KnowledgeDerived, Text: path.Base(u.Text), Assumptions: []string{"remote name derived from the URL path"}}
				if !u.Concrete() {
					name = model.Symbolic("<remote-name>")
				}
				resources = append(resources, cmd.Resource(name, "destination"))
			}
			a := rules.NewAction(kind, model.BasisSyntax, resources...)
			a.Network = &model.NetworkDetail{URL: u, Method: method}
			if !u.Concrete() {
				a.Kind = model.NetUnresolved
			}
			a.Evidence.Assumptions = append(a.Evidence.Assumptions, "redirects, proxies and response content are unknown")
			res.Actions = append(res.Actions, withAssumptions(a, o))
		}
		if len(urls) == 0 {
			a := rules.NewAction(model.NetUnresolved, model.BasisSyntax)
			a.Network = &model.NetworkDetail{URL: model.Unknown()}
			res.Actions = append(res.Actions, a)
		}
		return res
	},
}

var wgetRule = rules.Rule{
	ID: "std/wget", Version: "1", Executables: []string{"wget"}, Platform: "",
	Doc: "wget [option]... [URL]...; -O file --output-document -P prefix -q -c -r --post-data --post-file",
	Apply: func(cmd rules.Command) rules.Result {
		o := rules.ParseOptions(cmd.Args, rules.OptionSpec{ShortFlags: "qvcrnpkmHbSFNEKLx46", ShortArgs: "OPoaTtwlUAReDiQB",
			LongArgs:  []string{"output-document", "directory-prefix", "output-file", "append-output", "timeout", "tries", "wait", "level", "user-agent", "accept", "reject", "domains", "exclude-domains", "input-file", "quota", "base", "post-data", "post-file", "header", "method", "body-data", "body-file", "user", "password", "ca-certificate", "limit-rate"},
			LongFlags: []string{"quiet", "verbose", "continue", "recursive", "no-parent", "convert-links", "mirror", "span-hosts", "server-response", "timestamping", "adjust-extension", "backup-converted", "relative", "force-directories", "no-check-certificate", "no-clobber", "spider", "background", "https-only", "show-progress", "no-verbose"}})
		res := rules.Result{Assumptions: o.Assumptions}
		for _, u := range o.Operands {
			kind := model.NetDownload
			var dest model.Value
			switch {
			case o.Has("spider"):
				kind = model.NetRequest
			default:
				if out, ok := o.Value("O"); ok {
					dest = out
				} else if out, ok := o.Value("output-document"); ok {
					dest = out
				} else {
					dest = model.Value{Knowledge: model.KnowledgeDerived, Text: path.Base(u.Text), Assumptions: []string{"file name derived from the URL path"}}
					if !u.Concrete() {
						dest = model.Symbolic("<remote-name>")
					}
					if p, ok := o.Value("P"); ok && p.Concrete() && dest.Concrete() {
						dest.Text = path.Join(p.Text, dest.Text)
					}
				}
			}
			var resources []model.Resource
			if kind == model.NetDownload {
				resources = append(resources, cmd.Resource(dest, "destination"))
			}
			if len(o.Values["post-data"])+len(o.Values["post-file"])+len(o.Values["body-data"])+len(o.Values["body-file"]) > 0 {
				kind = model.NetUpload
			}
			a := rules.NewAction(kind, model.BasisSyntax, resources...)
			a.Network = &model.NetworkDetail{URL: u}
			if !u.Concrete() {
				a.Kind = model.NetUnresolved
			}
			a.Evidence.Assumptions = append(a.Evidence.Assumptions, "redirects, proxies and response content are unknown")
			res.Actions = append(res.Actions, withAssumptions(a, o))
		}
		return res
	},
}
