package core

import (
	"strings"
	"testing"
)

// promptContractOwner is the package that builds the SPEC §9.2 prompt contract from a card, a unit
// and a run of one repository; the module table names it as the one owner of that work.
const promptContractOwner = "internal/engine"

// TestModuleMapNamesOneOwnerOfPromptContractAssembly proves the module table names engine, and only
// engine, as the owner of prompt contract assembly (SPEC §6.1, §9.2, DR-17). Roles holds the cards
// and the card schema, so its row may not claim the contract, and no second row may copy it.
func TestModuleMapNamesOneOwnerOfPromptContractAssembly(t *testing.T) {
	t.Parallel()
	var owners []string
	for _, m := range Modules() {
		if strings.Contains(strings.ToLower(m.Responsibility), "prompt contract") {
			owners = append(owners, m.Package)
		}
	}
	if len(owners) != 1 || owners[0] != promptContractOwner {
		t.Fatalf("the module table names %v as the owner of prompt contract assembly, want [%s]",
			owners, promptContractOwner)
	}
}
