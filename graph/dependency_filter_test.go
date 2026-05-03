package graph

import "testing"

func TestClassifyDependency(t *testing.T) {
	tests := []struct {
		module string
		want   DependencyClass
	}{
		// Internal (relative imports)
		{"./pricing.engine", DepInternal},
		{"../shared/utils", DepInternal},

		// Infrastructure (skip entirely)
		{"lodash", DepInfrastructure},
		{"@types/node", DepInfrastructure},
		{"typescript", DepInfrastructure},
		{"jest", DepInfrastructure},
		{"prettier", DepInfrastructure},
		{"uuid", DepInfrastructure},
		{"rxjs", DepInfrastructure},
		{"dotenv", DepInfrastructure},
		{"class-validator", DepInfrastructure},

		// Architectural (track as metadata, not edges)
		{"@nestjs/common", DepArchitectural},
		{"@nestjs/core", DepArchitectural},
		{"@prisma/client", DepArchitectural},
		{"kafkajs", DepArchitectural},
		{"ioredis", DepArchitectural},
		{"graphql", DepArchitectural},
		{"typeorm", DepArchitectural},
		{"mongoose", DepArchitectural},

		// Unknown external → treated as Internal (track it)
		{"@myorg/pricing-engine", DepInternal},
		{"@otopoint/shared", DepInternal},
		{"some-unknown-lib", DepInternal},
	}

	for _, tt := range tests {
		got := ClassifyDependency(tt.module)
		if got != tt.want {
			t.Errorf("ClassifyDependency(%q) = %d, want %d", tt.module, got, tt.want)
		}
	}
}

func TestShouldTrackDependency(t *testing.T) {
	// Should track: internal and unknown
	if !ShouldTrackDependency("./pricing") {
		t.Error("relative import should be tracked")
	}
	if !ShouldTrackDependency("@myorg/shared-lib") {
		t.Error("unknown scoped package should be tracked")
	}

	// Should NOT track: infrastructure
	if ShouldTrackDependency("lodash") {
		t.Error("lodash should not be tracked")
	}
	if ShouldTrackDependency("@types/express") {
		t.Error("@types/* should not be tracked")
	}

	// Should NOT track: architectural (creates noise)
	if ShouldTrackDependency("@nestjs/common") {
		t.Error("@nestjs/common should not create edges")
	}
	if ShouldTrackDependency("@prisma/client") {
		t.Error("@prisma/client should not create edges")
	}
}
