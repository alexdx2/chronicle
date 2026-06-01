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
	if !foundCatToWeapon {
		t.Error("missing Cat -> CatWeapon array relation")
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
