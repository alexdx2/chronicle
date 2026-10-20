package extract

import (
	"regexp"
	"strings"

	"github.com/alexdx2/chronicle-core/truth"
)

// Redis streams (kind "stream": XADD produces, XREAD / XREADGROUP consume) and
// pub/sub (kind "topic": PUBLISH produces, SUBSCRIBE / PSUBSCRIBE consume).
// JavaScript (ioredis, node-redis), Python (redis-py, aioredis) and .NET
// (StackExchange.Redis) are covered; a file must import the client. Only
// literal key and channel names count.
//
// .NET Subscribe is matched only in its async form: the synchronous call is
// too common outside Redis to read without more context.

var (
	redisJSImport = chJSImport(`ioredis|redis|@redis/[\w.-]+|@upstash/redis|@nestjs-modules/ioredis`)
	redisPyImport = chPyImport(`redis|aioredis`)
	redisCsImport = regexp.MustCompile(`\busing\s+StackExchange\.Redis\b`)

	redisKeyProp = regexp.MustCompile(`\bkey\s*:\s*(['"])([^'"]+)['"]`)
	redisPyDict  = regexp.MustCompile(`[{,]\s*(['"])([^'"]+)['"]\s*:`)
	csChannelLit = regexp.MustCompile(`^(?:new\s+RedisChannel\(\s*|RedisChannel\.(?:Literal|Pattern)\(\s*)?"([^"]+)"\s*(?:,[^)]*)?\)?$`)
)

func init() {
	Rules = append(Rules,
		funcRule{"redis.js", func(p string) bool { return hasExt(p, ".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs") }, applyRedisJS},
		funcRule{"redis.py", func(p string) bool { return hasExt(p, ".py") }, applyRedisPy},
		funcRule{"redis.dotnet", func(p string) bool { return hasExt(p, ".cs") }, applyRedisCs},
	)
}

// redisStreamsKeyword returns the keys that follow an ioredis STREAMS marker:
// the first half of the remaining arguments (keys, then ids). nil when there
// is no marker or the rest does not split in half.
func redisStreamsKeyword(args []string) ([]string, bool) {
	for i, a := range args {
		if s, ok := chLit(a); ok && strings.EqualFold(s, "STREAMS") {
			rest := args[i+1:]
			if len(rest) == 0 || len(rest)%2 != 0 {
				return nil, true
			}
			var out []string
			for _, k := range rest[:len(rest)/2] {
				if s, ok := chLit(k); ok {
					out = append(out, s)
				}
			}
			return out, true
		}
	}
	return nil, false
}

// redisJSStreams: ioredis ('STREAMS', 'k', id) or node-redis ({ key: 'k', id }).
func redisJSStreams(c chCall) []string {
	if keys, ok := redisStreamsKeyword(c.args); ok {
		return keys
	}
	var out []string
	for _, a := range c.args {
		for _, m := range redisKeyProp.FindAllStringSubmatch(a, -1) {
			out = append(out, m[2])
		}
	}
	return out
}

// redisPyStreams: xread({'k': '$'}) / xreadgroup(g, c, {'k': '>'}) / streams={...}.
func redisPyStreams(c chCall) []string {
	var out []string
	for _, a := range c.args {
		t := strings.TrimSpace(a)
		if strings.HasPrefix(t, "streams") {
			t = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(t, "streams"), "="))
		}
		if !strings.HasPrefix(t, "{") {
			continue
		}
		for _, m := range redisPyDict.FindAllStringSubmatch(t, -1) {
			out = append(out, m[2])
		}
	}
	return out
}

func applyRedisJS(f *FileCtx) []truth.Fact {
	return chRule("redis.js", chImports(redisJSImport),
		chSpec{names: `(?i:xadd)`, kind: "stream", side: "produce", pick: chFirst("")},
		chSpec{names: `(?i:xreadgroup|xread)`, kind: "stream", side: "consume", pick: redisJSStreams},
		chSpec{names: `publish`, kind: "topic", side: "produce", pick: chFirst("")},
		chSpec{names: `(?i:subscribe|psubscribe)`, kind: "topic", side: "consume", pick: func(c chCall) []string { return chLeadLits(c.args) }},
	)(f)
}

func applyRedisPy(f *FileCtx) []truth.Fact {
	return chRule("redis.py", chImports(redisPyImport),
		chSpec{names: `xadd`, kind: "stream", side: "produce", pick: chFirst("name")},
		chSpec{names: `xreadgroup|xread`, kind: "stream", side: "consume", pick: redisPyStreams},
		chSpec{names: `publish`, kind: "topic", side: "produce", pick: chFirst("channel")},
		chSpec{names: `subscribe|psubscribe`, kind: "topic", side: "consume", pick: func(c chCall) []string { return chLeadLits(c.args) }},
	)(f)
}

// csChannel reads the channel of a .NET call: "c", new RedisChannel("c", ...)
// or RedisChannel.Literal("c").
func csChannel(c chCall) []string {
	if len(c.args) == 0 {
		return nil
	}
	a := strings.TrimSpace(c.args[0])
	if m := csChannelLit.FindStringSubmatch(a); m != nil {
		return []string{m[1]}
	}
	return nil
}

func csKey(c chCall) []string {
	if len(c.args) == 0 {
		return nil
	}
	if s, ok := chLit(c.args[0]); ok && strings.HasPrefix(strings.TrimSpace(c.args[0]), `"`) {
		return []string{s}
	}
	return nil
}

func applyRedisCs(f *FileCtx) []truth.Fact {
	return chRule("redis.dotnet", chImports(redisCsImport),
		chSpec{names: `StreamAdd(?:Async)?`, kind: "stream", side: "produce", pick: csKey},
		chSpec{names: `StreamRead(?:Group)?(?:Async)?`, kind: "stream", side: "consume", pick: csKey},
		chSpec{names: `Publish(?:Async)?`, kind: "topic", side: "produce", pick: csChannel},
		chSpec{names: `SubscribeAsync`, kind: "topic", side: "consume", pick: csChannel},
	)(f)
}
