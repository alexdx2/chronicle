package extract

import (
	"regexp"
	"strings"

	"github.com/alexdx2/chronicle-core/truth"
	"github.com/alexdx2/chronicle-core/truth/extract/lex"
)

// Messaging channels: topics, queues and event buses with a literal name.
// Only string literals count; names from variables or interpolations yield
// no fact. Rules return nil for test files.

// channelName normalises a literal for the key: lower case, spaces to "-".
// It returns "" for empty or interpolated names.
func channelName(lit string) string {
	lit = strings.TrimSpace(lit)
	if lit == "" || strings.ContainsAny(lit, "${}%") {
		return ""
	}
	return strings.ReplaceAll(strings.ToLower(lit), " ", "-")
}

func channelFact(rule string, f *FileCtx, l lex.Line, kind, lit, side string, derived ...truth.Evidence) (truth.Fact, bool) {
	n := channelName(lit)
	if n == "" {
		return truth.Fact{}, false
	}
	return truth.Fact{
		Class:   truth.ClassChannels,
		Key:     truth.ChannelKey(kind, n, side),
		Attrs:   map[string]string{"name": strings.TrimSpace(lit), "kind": kind, "side": side},
		Primary: evidence(f, l),
		Derived: derived,
		Sources: []string{rule},
	}, true
}

// codeWindow joins the code of lines[i : i+n].
func codeWindow(lines []lex.Line, i, n int) string {
	var sb strings.Builder
	for j := i; j < len(lines) && j < i+n; j++ {
		sb.WriteString(lines[j].Code)
		sb.WriteByte(' ')
	}
	return sb.String()
}

// sideAround guesses produce/consume from send/receive calls within 6 lines
// either way of line i; ambiguous or silent windows are "declare".
func sideAround(lines []lex.Line, i int) string {
	var send, recv bool
	for j := i - 6; j <= i+6; j++ {
		if j < 0 || j >= len(lines) {
			continue
		}
		c := strings.ToLower(lines[j].Code)
		send = send || strings.Contains(c, "sendmessage") || strings.Contains(c, "send_message")
		recv = recv || strings.Contains(c, "receivemessage") || strings.Contains(c, "receive_message")
	}
	switch {
	case send && !recv:
		return "produce"
	case recv && !send:
		return "consume"
	}
	return "declare"
}

// chanSpec is one regex whose capture 1 is a name (or, with list set, a
// fragment holding several quoted names).
type chanSpec struct {
	re   *regexp.Regexp
	kind string
	side string
	list bool
	win  int // lines joined for the match (default 1)
}

var quotedLit = regexp.MustCompile(`['"]([^'"]+)['"]`)

func specRule(name string, pre func(f *FileCtx) bool, specs ...chanSpec) func(f *FileCtx) []truth.Fact {
	return func(f *FileCtx) []truth.Fact {
		if f.IsTest || (pre != nil && !pre(f)) {
			return nil
		}
		var out []truth.Fact
		for i, l := range f.Lines {
			for _, s := range specs {
				text := l.Code
				if s.win > 1 {
					text = codeWindow(f.Lines, i, s.win)
				}
				loc := s.re.FindStringSubmatchIndex(text)
				if loc == nil || loc[0] >= len(l.Code) { // multi-line: match must start on this line
					continue
				}
				m := make([]string, len(loc)/2)
				for k := range m {
					if loc[2*k] >= 0 {
						m[k] = text[loc[2*k]:loc[2*k+1]]
					}
				}
				var names []string
				if s.list {
					for _, q := range quotedLit.FindAllStringSubmatch(m[1], -1) {
						names = append(names, q[1])
					}
				} else {
					names = []string{m[1]}
				}
				for _, n := range names {
					if fc, ok := channelFact(name, f, l, s.kind, n, s.side); ok {
						out = append(out, fc)
					}
				}
			}
		}
		return out
	}
}

const cq = "['\"`]"

var (
	sqsURLName = regexp.MustCompile(`^https?://(?:sqs\.[^/]+/[^/]+|[^/]+/\d{12})/([^/?#]+)$`)

	csSubOne   = regexp.MustCompile(`\.Subscribe\(\s*"([^"]+)"\s*\)`)
	csSubArr   = regexp.MustCompile(`\.Subscribe\(\s*new\s*(?:string)?\s*\[\]\s*\{([^}]*)\}`)
	csProduce  = regexp.MustCompile(`\.Produce(?:Async)?\(\s*"([^"]+)"`)
	csTopicPar = regexp.MustCompile(`new\s+TopicPartition\(\s*"([^"]+)"`)

	csQueueURL    = regexp.MustCompile(`\bQueueUrl\s*=\s*"([^"]+)"`)
	csGetQueueURL = regexp.MustCompile(`\bGetQueueUrlAsync\(\s*"([^"]+)"\s*\)`)
	csDetailType  = regexp.MustCompile(`\bDetailType\s*=\s*"([^"]+)"`)

	kjsSub  = regexp.MustCompile(`\bsubscribe\(\s*\{[^}]*?\btopics?:\s*(?:` + cq + `([^'"` + "`" + `]*)` + cq + `|\[([^\]]*)\])`)
	kjsSend = regexp.MustCompile(`\.send\(\s*\{[^}]*?\btopic:\s*` + cq + `([^'"` + "`" + `]*)` + cq)
	nestPat = regexp.MustCompile(`@(?:EventPattern|MessagePattern)\(\s*['"]([^'"]+)['"]\s*[,)]`)
	jsQueue = regexp.MustCompile(`\bQueueUrl:\s*['"](https?://[^'"]+)['"]`)

	pyConsumer  = regexp.MustCompile(`\bKafkaConsumer\(((?:\s*['"][^'"]+['"]\s*,?)+)`)
	pySubList   = regexp.MustCompile(`\.subscribe\(\s*(?:topics\s*=\s*)?\[([^\]]*)\]`)
	pySubOne    = regexp.MustCompile(`\.subscribe\(\s*(?:topics\s*=\s*)?(['"][^'"]+['"])\s*[,)]`)
	pySend      = regexp.MustCompile(`\.send\(\s*(['"][^'"]+['"])`)
	pyQueueName = regexp.MustCompile(`\bget_queue_url\(\s*QueueName\s*=\s*['"]([^'"]+)['"]`)
	pyQueueURL  = regexp.MustCompile(`\bQueueUrl\s*=\s*['"](https?://[^'"]+)['"]`)
)

// urlRule handles "QueueUrl = <url>" style matches where the side depends on
// neighbouring send/receive calls.
func urlRule(name string, re *regexp.Regexp) func(f *FileCtx) []truth.Fact {
	return func(f *FileCtx) []truth.Fact {
		if f.IsTest {
			return nil
		}
		var out []truth.Fact
		for i, l := range f.Lines {
			for _, m := range re.FindAllStringSubmatch(l.Code, -1) {
				u := sqsURLName.FindStringSubmatch(m[1])
				if u == nil {
					continue
				}
				if fc, ok := channelFact(name, f, l, "queue", u[1], sideAround(f.Lines, i)); ok {
					out = append(out, fc)
				}
			}
		}
		return out
	}
}

func isKafkaPy(f *FileCtx) bool { return codeContains(f, "kafka") }

func init() {
	isCS := func(p string) bool { return hasExt(p, ".cs") }
	isJS := func(p string) bool { return hasExt(p, ".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs") }
	isPy := func(p string) bool { return hasExt(p, ".py") }
	Rules = append(Rules,
		funcRule{"kafka.dotnet", isCS, specRule("kafka.dotnet", nil,
			chanSpec{re: csSubOne, kind: "topic", side: "consume"},
			chanSpec{re: csSubArr, kind: "topic", side: "consume", list: true},
			chanSpec{re: csProduce, kind: "topic", side: "produce"},
			chanSpec{re: csTopicPar, kind: "topic", side: "consume"})},
		funcRule{"sqs.dotnet", isCS, applySQSDotnet},
		funcRule{"eventbridge.dotnet", isCS, specRule("eventbridge.dotnet", nil,
			chanSpec{re: csDetailType, kind: "bus", side: "produce"})},
		funcRule{"kafkajs", isJS, applyKafkaJS},
		funcRule{"nest.pattern", isJS, specRule("nest.pattern", nil,
			chanSpec{re: nestPat, kind: "topic", side: "consume"})},
		funcRule{"sqs.js", isJS, urlRule("sqs.js", jsQueue)},
		funcRule{"kafka.py", isPy, specRule("kafka.py", isKafkaPy,
			chanSpec{re: pyConsumer, kind: "topic", side: "consume", list: true, win: 3},
			chanSpec{re: pySubList, kind: "topic", side: "consume", list: true, win: 3},
			chanSpec{re: pySubOne, kind: "topic", side: "consume", list: true},
			chanSpec{re: pySend, kind: "topic", side: "produce", list: true})},
		funcRule{"boto3.sqs", isPy, applyBoto3},
	)
}

// applySQSDotnet: QueueUrl with a side from nearby calls, plus GetQueueUrlAsync.
func applySQSDotnet(f *FileCtx) []truth.Fact {
	out := urlRule("sqs.dotnet", csQueueURL)(f)
	return append(out, specRule("sqs.dotnet", nil, chanSpec{re: csGetQueueURL, kind: "queue", side: "declare"})(f)...)
}

func applyBoto3(f *FileCtx) []truth.Fact {
	out := urlRule("boto3.sqs", pyQueueURL)(f)
	return append(out, specRule("boto3.sqs", nil, chanSpec{re: pyQueueName, kind: "queue", side: "declare", win: 2})(f)...)
}

// applyKafkaJS matches subscribe/send object literals over a 4-line window.
func applyKafkaJS(f *FileCtx) []truth.Fact {
	if f.IsTest {
		return nil
	}
	var out []truth.Fact
	for i, l := range f.Lines {
		switch {
		case strings.Contains(l.Code, "subscribe("):
			m := kjsSub.FindStringSubmatch(codeWindow(f.Lines, i, 4))
			if m == nil {
				continue
			}
			names := []string{m[1]}
			if m[2] != "" {
				names = names[:0]
				for _, q := range quotedLit.FindAllStringSubmatch(m[2], -1) {
					names = append(names, q[1])
				}
			}
			for _, n := range names {
				if fc, ok := channelFact("kafkajs", f, l, "topic", n, "consume"); ok {
					out = append(out, fc)
				}
			}
		case strings.Contains(l.Code, ".send("):
			if m := kjsSend.FindStringSubmatch(codeWindow(f.Lines, i, 4)); m != nil {
				if fc, ok := channelFact("kafkajs", f, l, "topic", m[1], "produce"); ok {
					out = append(out, fc)
				}
			}
		}
	}
	return out
}
