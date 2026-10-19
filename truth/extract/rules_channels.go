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

// codeWindow joins the code of lines[i : i+n] unconditionally. callText (in
// rules_common.go) instead stops when the call's parentheses balance, which
// suits one call but not object literals or Python keyword arguments.
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
	// sideFn, when set, derives the side from the code before the match
	// (up to 3 preceding lines and the start of this one).
	sideFn func(prefix string) string
	// gate, when set, means capture 1 is a receiver identifier and capture 2
	// the name(s); the match counts only if gate(f, receiver) is true.
	gate func(f *FileCtx, recv string) bool
}

// literalItems returns the plain string literals of a bracket fragment such as
// `"a", 'b'`. An element that is not exactly one literal (`"a-" + env`,
// `prefix + "b"`, a call) yields nothing, so a concatenation never leaks its
// literal half as a channel name. Trailing closers of the enclosing call or
// list (`)`, `]`, `}`) after a literal are tolerated.
func literalItems(frag string) []string {
	var parts []string
	start, quote := 0, byte(0)
	for i := 0; i < len(frag); i++ {
		c := frag[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '\'' || c == '"':
			quote = c
		case c == ',':
			parts = append(parts, frag[start:i])
			start = i + 1
		}
	}
	parts = append(parts, frag[start:])
	var out []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if len(p) < 3 || (p[0] != '\'' && p[0] != '"') {
			continue
		}
		j := strings.IndexByte(p[1:], p[0])
		if j <= 0 {
			continue
		}
		if strings.Trim(p[j+2:], ")]} \t") != "" {
			continue
		}
		out = append(out, p[1:j+1])
	}
	return out
}

// enclosingCall returns the identifier of the innermost call whose parenthesis
// is still open at the end of prefix ("" when there is none).
func enclosingCall(prefix string) string {
	depth := 0
	for i := len(prefix) - 1; i >= 0; i-- {
		switch prefix[i] {
		case ')':
			depth++
		case '(':
			if depth > 0 {
				depth--
				continue
			}
			j := i
			for j > 0 && (isIdentByte(prefix[j-1])) {
				j--
			}
			return prefix[j:i]
		}
	}
	return ""
}

func isIdentByte(b byte) bool {
	return b == '_' || b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
}

// topicPartitionSide classifies `new TopicPartition(...)` by the call that
// encloses it: Assign/Seek/Commit consume, Produce* produce, else declare.
func topicPartitionSide(prefix string) string {
	switch enclosingCall(prefix) {
	case "Assign", "Incremental_Assign", "IncrementalAssign", "Seek", "Commit", "Pause", "Resume":
		return "consume"
	case "Produce", "ProduceAsync":
		return "produce"
	}
	return "declare"
}

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
				if s.gate != nil {
					if !s.gate(f, m[1]) {
						continue
					}
					m = m[1:]
				}
				var names []string
				if s.list {
					names = literalItems(m[1])
				} else {
					names = []string{m[1]}
				}
				side := s.side
				if s.sideFn != nil {
					var pre strings.Builder
					for j := i - 3; j < i; j++ {
						if j >= 0 {
							pre.WriteString(f.Lines[j].Code)
							pre.WriteByte(' ')
						}
					}
					pre.WriteString(l.Code[:loc[0]])
					side = s.sideFn(pre.String())
				}
				for _, n := range names {
					if fc, ok := channelFact(name, f, l, s.kind, n, side); ok {
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
	csProduce  = regexp.MustCompile(`\.Produce(?:Async)?\(\s*"([^"]+)"\s*[,)]`)
	csTopicPar = regexp.MustCompile(`new\s+TopicPartition\(\s*"([^"]+)"\s*,`)

	csQueueURL    = regexp.MustCompile(`\bQueueUrl\s*=\s*"([^"]+)"\s*(?:[,;})]|$)`)
	csGetQueueURL = regexp.MustCompile(`\bGetQueueUrlAsync\(\s*"([^"]+)"\s*\)`)
	csDetailType  = regexp.MustCompile(`\bDetailType\s*=\s*"([^"]+)"\s*(?:[,;})]|$)`)

	kjsSub  = regexp.MustCompile(`\bsubscribe\(\s*\{[^}]*?\btopics?:\s*(?:` + cq + `([^'"` + "`" + `]*)` + cq + `\s*[,}]|\[([^\]]*)\])`)
	kjsSend = regexp.MustCompile(`\.send\(\s*\{[^}]*?\btopic:\s*` + cq + `([^'"` + "`" + `]*)` + cq + `\s*[,}]`)
	nestPat = regexp.MustCompile(`@(?:EventPattern|MessagePattern)\(\s*['"]([^'"]+)['"]\s*[,)]`)
	jsQueue = regexp.MustCompile(`\bQueueUrl:\s*['"](https?://[^'"]+)['"]\s*(?:[,;})]|$)`)

	pyConsumer  = regexp.MustCompile(`\bKafkaConsumer\(((?:\s*['"][^'"]+['"]\s*(?:,|\)))+)`)
	pySubList   = regexp.MustCompile(`([A-Za-z_][\w.]*)\.subscribe\(\s*(?:topics\s*=\s*)?\[([^\]]*)\]`)
	pySubOne    = regexp.MustCompile(`([A-Za-z_][\w.]*)\.subscribe\(\s*(?:topics\s*=\s*)?(['"][^'"]+['"])\s*[,)]`)
	pySend      = regexp.MustCompile(`([A-Za-z_][\w.]*)\.send\(\s*(?:topic\s*=\s*)?(['"][^'"]+['"])\s*[,)]`)
	pyQueueName = regexp.MustCompile(`\bget_queue_url\(\s*QueueName\s*=\s*['"]([^'"]+)['"]\s*[,)]`)
	pyQueueURL  = regexp.MustCompile(`\bQueueUrl\s*=\s*['"](https?://[^'"]+)['"]\s*[,)]`)
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

// isKafkaDotnet / isEventBridgeDotnet gate rules whose call shapes are shared
// with unrelated libraries (Redis Subscribe, plain DetailType fields).
func isKafkaDotnet(f *FileCtx) bool { return codeContains(f, "kafka") }
func isEventBridgeDotnet(f *FileCtx) bool {
	return codeContains(f, "putevents", "eventbridge", "cloudwatchevents")
}

var (
	pyKafkaAssign = regexp.MustCompile(`([A-Za-z_][\w.]*)\s*=\s*(?:await\s+)?(?:\w+\.)?(?:AIO)?Kafka(?:Producer|Consumer)\(`)
	pyKafkaImport = regexp.MustCompile(`(?i)\b(?:import|from)\b.*kafka`)
)

// pyKafkaReceiver accepts a receiver assigned from a Kafka client constructor
// in this file, or the conventional producer/consumer names when the file
// imports kafka.
func pyKafkaReceiver(f *FileCtx, recv string) bool {
	imports := false
	for _, l := range f.Lines {
		if m := pyKafkaAssign.FindStringSubmatch(l.Code); m != nil && m[1] == recv {
			return true
		}
		if pyKafkaImport.MatchString(l.Code) {
			imports = true
		}
	}
	switch recv {
	case "producer", "consumer", "self.producer", "self.consumer":
		return imports
	}
	return false
}

func init() {
	isCS := func(p string) bool { return hasExt(p, ".cs") }
	isJS := func(p string) bool { return hasExt(p, ".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs") }
	isPy := func(p string) bool { return hasExt(p, ".py") }
	Rules = append(Rules,
		funcRule{"kafka.dotnet", isCS, specRule("kafka.dotnet", isKafkaDotnet,
			chanSpec{re: csSubOne, kind: "topic", side: "consume"},
			chanSpec{re: csSubArr, kind: "topic", side: "consume", list: true},
			chanSpec{re: csProduce, kind: "topic", side: "produce"},
			chanSpec{re: csTopicPar, kind: "topic", sideFn: topicPartitionSide})},
		funcRule{"sqs.dotnet", isCS, applySQSDotnet},
		// Keyed by DetailType: EventBusName is almost always a variable.
		funcRule{"eventbridge.dotnet", isCS, specRule("eventbridge.dotnet", isEventBridgeDotnet,
			chanSpec{re: csDetailType, kind: "bus", side: "produce"})},
		funcRule{"kafkajs", isJS, applyKafkaJS},
		funcRule{"nest.pattern", isJS, specRule("nest.pattern", nil,
			chanSpec{re: nestPat, kind: "topic", side: "consume"})},
		funcRule{"sqs.js", isJS, urlRule("sqs.js", jsQueue)},
		funcRule{"kafka.py", isPy, specRule("kafka.py", isKafkaPy,
			chanSpec{re: pyConsumer, kind: "topic", side: "consume", list: true, win: 3},
			chanSpec{re: pySubList, kind: "topic", side: "consume", list: true, win: 3, gate: pyKafkaReceiver},
			chanSpec{re: pySubOne, kind: "topic", side: "consume", list: true, gate: pyKafkaReceiver},
			chanSpec{re: pySend, kind: "topic", side: "produce", list: true, win: 3, gate: pyKafkaReceiver})},
		funcRule{"boto3.sqs", isPy, applyBoto3},
	)
}

// applySQSDotnet: QueueUrl with a side from nearby calls, plus GetQueueUrlAsync.
func applySQSDotnet(f *FileCtx) []truth.Fact {
	return append(sqsDotnetURL(f), sqsDotnetName(f)...)
}

func applyBoto3(f *FileCtx) []truth.Fact {
	return append(boto3URL(f), boto3Name(f)...)
}

var (
	sqsDotnetURL  = urlRule("sqs.dotnet", csQueueURL)
	sqsDotnetName = specRule("sqs.dotnet", nil, chanSpec{re: csGetQueueURL, kind: "queue", side: "declare"})
	boto3URL      = urlRule("boto3.sqs", pyQueueURL)
	boto3Name     = specRule("boto3.sqs", nil, chanSpec{re: pyQueueName, kind: "queue", side: "declare", win: 2})
)

// applyKafkaJS matches subscribe/send object literals over a 4-line window.
func applyKafkaJS(f *FileCtx) []truth.Fact {
	if f.IsTest {
		return nil
	}
	var out []truth.Fact
	for i, l := range f.Lines {
		switch {
		case strings.Contains(l.Code, "subscribe("):
			text := codeWindow(f.Lines, i, 4)
			loc := kjsSub.FindStringSubmatchIndex(text)
			if loc == nil || loc[0] >= len(l.Code) { // must start on this line
				continue
			}
			m := make([]string, len(loc)/2)
			for k := range m {
				if loc[2*k] >= 0 {
					m[k] = text[loc[2*k]:loc[2*k+1]]
				}
			}
			names := []string{m[1]}
			if m[2] != "" {
				names = names[:0]
				names = literalItems(m[2])
			}
			for _, n := range names {
				if fc, ok := channelFact("kafkajs", f, l, "topic", n, "consume"); ok {
					out = append(out, fc)
				}
			}
		case strings.Contains(l.Code, ".send("):
			text := codeWindow(f.Lines, i, 4)
			if loc := kjsSend.FindStringSubmatchIndex(text); loc != nil && loc[0] < len(l.Code) {
				if fc, ok := channelFact("kafkajs", f, l, "topic", text[loc[2]:loc[3]], "produce"); ok {
					out = append(out, fc)
				}
			}
		}
	}
	return out
}
