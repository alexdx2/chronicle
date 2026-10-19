package extract

import (
	"regexp"
	"strings"

	"github.com/alexdx2/chronicle-core/truth"
	"github.com/alexdx2/chronicle-core/truth/extract/lex"
)

// Protocol Buffers: every rpc of a service is an endpoint "RPC Service/Method".

func init() {
	Rules = append(Rules,
		funcRule{"proto.rpc", func(p string) bool { return hasExt(p, ".proto") && !isThirdPartyProto(p) }, applyProtoRPC},
	)
}

func isThirdPartyProto(p string) bool {
	for _, d := range strings.Split(strings.ToLower(p), "/") {
		if d == "third_party" || d == "third-party" || d == "thirdparty" {
			return true
		}
	}
	return false
}

var (
	protoService = regexp.MustCompile(`\bservice\s+(\w+)\s*\{`)
	protoRPC     = regexp.MustCompile(`\brpc\s+(\w+)\s*\(`)
)

func applyProtoRPC(f *FileCtx) []truth.Fact {
	if isTestLike(f) {
		return nil
	}
	var out []truth.Fact
	depth, svcDepth := 0, -1
	svc := ""
	for _, l := range f.Lines {
		code := l.Code
		mask := litMask(code, false)
		type ev struct {
			pos  int
			kind byte
			name string
		}
		var evs []ev
		for _, m := range protoService.FindAllStringSubmatchIndex(code, -1) {
			if !mask[m[0]] {
				evs = append(evs, ev{m[0], 's', code[m[2]:m[3]]})
			}
		}
		for _, m := range protoRPC.FindAllStringSubmatchIndex(code, -1) {
			if !mask[m[0]] {
				evs = append(evs, ev{m[0], 'r', code[m[2]:m[3]]})
			}
		}
		// order events by position
		for a := 1; a < len(evs); a++ {
			for b := a; b > 0 && evs[b].pos < evs[b-1].pos; b-- {
				evs[b], evs[b-1] = evs[b-1], evs[b]
			}
		}
		next := 0
		for i := 0; i <= len(code); i++ {
			for next < len(evs) && evs[next].pos == i {
				e := evs[next]
				next++
				switch e.kind {
				case 's':
					svc, svcDepth = e.name, depth // the "{" of the service raises depth by one
				case 'r':
					if svc != "" && depth > svcDepth {
						out = append(out, protoFact(f, l, svc, e.name))
					}
				}
			}
			if i == len(code) || mask[i] {
				continue
			}
			switch code[i] {
			case '{':
				depth++
			case '}':
				depth--
				if svc != "" && depth <= svcDepth {
					svc, svcDepth = "", -1
				}
			}
		}
	}
	return out
}

func protoFact(f *FileCtx, l lex.Line, svc, method string) truth.Fact {
	return truth.Fact{
		Class:   truth.ClassEndpoints,
		Key:     "RPC " + svc + "/" + method,
		Attrs:   map[string]string{"method": "RPC", "path": "/" + svc + "/" + method},
		Primary: evidence(f, l),
		Sources: []string{"proto.rpc"},
	}
}
