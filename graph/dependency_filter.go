package graph

import "strings"

// DependencyClass determines how a dependency should be treated in the graph.
type DependencyClass int

const (
	// DepInternal — project's own code. Always track.
	DepInternal DependencyClass = iota
	// DepArchitectural — frameworks/libs that shape architecture (NestJS, Prisma, Kafka). Track as evidence context, not as separate nodes.
	DepArchitectural
	// DepInfrastructure — system deps everyone uses (lodash, express internals, typescript). Skip entirely.
	DepInfrastructure
)

// ClassifyDependency determines whether a dependency is worth tracking in the graph.
func ClassifyDependency(module string) DependencyClass {
	// Internal: relative imports or workspace packages
	if strings.HasPrefix(module, "./") || strings.HasPrefix(module, "../") {
		return DepInternal
	}

	// Check against known infrastructure (not architecturally interesting)
	if isInfrastructure(module) {
		return DepInfrastructure
	}

	// Check against known architectural frameworks
	if isArchitectural(module) {
		return DepArchitectural
	}

	// Unknown external — might be interesting, treat as internal for safety
	return DepInternal
}

// isInfrastructure returns true for deps that add no architectural insight.
// These are utility libraries, type packages, dev tools — things that appear
// in every project and tell you nothing about how the system is structured.
func isInfrastructure(module string) bool {
	for _, prefix := range infrastructurePrefixes {
		if strings.HasPrefix(module, prefix) {
			return true
		}
	}
	for _, exact := range infrastructureExact {
		if module == exact {
			return true
		}
	}
	return false
}

// isArchitectural returns true for deps that define architecture patterns.
// These are tracked differently — as evidence metadata on the source node,
// not as separate graph nodes with edges.
func isArchitectural(module string) bool {
	for _, prefix := range architecturalPrefixes {
		if strings.HasPrefix(module, prefix) {
			return true
		}
	}
	return false
}

// Infrastructure: skip entirely. No nodes, no edges.
var infrastructurePrefixes = []string{
	// JS/TS utilities
	"lodash", "underscore", "ramda",
	"moment", "dayjs", "date-fns",
	"uuid", "nanoid",
	"path", "fs", "os", "util", "crypto", "stream", "events", "buffer", "url", "net", "http", "https",
	"assert", "child_process", "cluster", "dns", "readline", "tty", "zlib",
	// Type packages
	"@types/",
	// Dev tools
	"eslint", "prettier", "jest", "mocha", "chai", "sinon", "vitest",
	"ts-node", "tsx", "tsup", "esbuild", "webpack", "vite", "rollup",
	"typescript",
	// Testing
	"supertest", "nock", "@testing-library",
	// Misc utilities
	"dotenv", "debug", "chalk", "colors", "commander", "yargs",
	"class-validator", "class-transformer",
	"rxjs",
}

var infrastructureExact = []string{
	"express", "koa", "fastify", // web frameworks are infra unless they ARE the service
	"react", "react-dom", "next", "vue", "angular",
	"node:fs", "node:path", "node:crypto", "node:events", "node:stream",
	// Go standard library
	"fmt", "log", "os", "io", "strings", "strconv", "errors",
	"context", "sync", "time", "encoding", "testing", "reflect", "unsafe", "runtime",
}

// Architectural: track as metadata/evidence context, not separate nodes.
// These tell you WHAT pattern is used but create noise as individual edges.
var architecturalPrefixes = []string{
	"@nestjs/",
	"@prisma/",
	"@grpc/",
	"kafkajs", "amqplib", "ioredis", "redis", "bullmq",
	"@apollo/", "graphql", "type-graphql",
	"socket.io", "@nestjs/websockets",
	"@nestjs/microservices",
	"typeorm", "sequelize", "knex", "drizzle",
	"mongoose", "mongodb",
	"pg", "mysql", "mysql2", "better-sqlite3",
	// Go architectural
	"google.golang.org/grpc",
	"github.com/gin-gonic/",
	"github.com/gorilla/",
	"github.com/labstack/echo",
	"gorm.io/",
}

// ShouldTrackDependency returns true if a dependency should create a graph edge.
// Internal deps always tracked. Architectural deps tracked only if project uses them
// as cross-service communication. Infrastructure deps never tracked.
func ShouldTrackDependency(module string) bool {
	class := ClassifyDependency(module)
	return class == DepInternal
}

// IsArchitecturalDep returns true if a dependency defines architectural patterns.
// These can be stored as node metadata (e.g. "uses NestJS", "uses Prisma")
// but don't create separate edges.
func IsArchitecturalDep(module string) bool {
	return ClassifyDependency(module) == DepArchitectural
}
