package prisma

import (
	"os"
	"testing"
)

func TestExtractModelsAndEnums(t *testing.T) {
	schema := []byte(`
datasource db {
  provider = "postgresql"
  url      = env("DATABASE_URL")
}

model BattleEvent {
  id       String       @id @default(uuid())
  tomId    String
  result   BattleResult
  damage   Int
}

model Cat {
  id       String     @id @default(uuid())
  name     String
  weapons  CatWeapon[]
}

model CatWeapon {
  id    String @id @default(uuid())
  cat   Cat    @relation(fields: [catId], references: [id])
  catId String
}

enum BattleResult {
  TOM_HITS
  JERRY_DODGES
}

enum WeaponType {
  SCRATCH
  BITE
}
`)
	result := Extract(schema)

	if len(result.Models) != 3 {
		t.Errorf("models: want 3, got %d", len(result.Models))
	}
	if len(result.Enums) != 2 {
		t.Errorf("enums: want 2, got %d", len(result.Enums))
	}

	// CatWeapon -> Cat relation (via @relation field type)
	foundCatWeaponToCat := false
	// Cat -> CatWeapon relation (via array field)
	foundCatToWeapon := false
	for _, r := range result.Relations {
		if r.From == "CatWeapon" && r.To == "Cat" {
			foundCatWeaponToCat = true
		}
		if r.From == "Cat" && r.To == "CatWeapon" {
			foundCatToWeapon = true
		}
	}
	if !foundCatWeaponToCat {
		t.Error("missing CatWeapon -> Cat relation")
	}
	// FK-side-only convention: the array (inverse) side must NOT emit a relation,
	// otherwise every relation is doubled.
	if foundCatToWeapon {
		t.Error("unexpected Cat -> CatWeapon relation from array side (inverse must be skipped)")
	}
}

func TestExtractScalarFields(t *testing.T) {
	schema := []byte(`
model Battle {
  id       String @id @default(uuid())
  winnerId String
  score    Int
  result   BattleResult
}

enum BattleResult {
  TOM_HITS
}
`)
	result := Extract(schema)
	if len(result.Models) != 1 {
		t.Fatalf("models: want 1, got %d", len(result.Models))
	}
	fields := result.Models[0].Fields
	byName := map[string]Field{}
	for _, f := range fields {
		byName[f.Name] = f
	}
	// Scalar fields must be kept (they used to be dropped) and tagged IsScalar.
	for _, name := range []string{"id", "winnerId", "score"} {
		f, ok := byName[name]
		if !ok {
			t.Errorf("scalar field %s missing", name)
			continue
		}
		if !f.IsScalar {
			t.Errorf("field %s should be IsScalar", name)
		}
	}
	// Enum-typed field is kept and NOT scalar.
	if f, ok := byName["result"]; !ok || f.IsScalar {
		t.Errorf("enum field result: ok=%v scalar=%v (want kept, non-scalar)", ok, ok && f.IsScalar)
	}
	// Scalar fields must never produce relations.
	if len(result.Relations) != 0 {
		t.Errorf("relations from scalar/enum fields: %v", result.Relations)
	}
}

func TestExtractArenaSchema(t *testing.T) {
	content, err := os.ReadFile("../../fixtures/tom-and-jerry/arena-api/prisma/schema.prisma")
	if err != nil {
		t.Skip("fixture not available")
	}
	result := Extract(content)
	if len(result.Models) < 1 {
		t.Errorf("arena models: want >= 1, got %d", len(result.Models))
	}
	if len(result.Enums) < 1 {
		t.Errorf("arena enums: want >= 1, got %d", len(result.Enums))
	}
}
