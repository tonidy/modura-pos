package httpapi

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"modura/apps/sales-go/internal/api"
	"modura/apps/sales-go/internal/testdb"
)

// TestKnowsNothingAboutModulesItDoesNotServe pins plan M7: the UI calls each service
// directly (ADR-0011), so this service neither forwards to nor describes modules
// another service owns. Its embedded spec may only name the modules it serves.
func TestKnowsNothingAboutModulesItDoesNotServe(t *testing.T) {
	spec, err := api.GetSwagger()
	if err != nil {
		t.Fatal(err)
	}
	served := map[string]bool{}
	for path := range spec.Paths.Map() {
		served[strings.SplitN(strings.TrimPrefix(path, "/v1/"), ".", 2)[0]] = true
	}

	raw, err := os.ReadFile(filepath.Join(testdb.Root(t), "generated", "ir", "modura.ir.json"))
	if err != nil {
		t.Fatal(err)
	}
	var ir struct {
		Modules map[string]json.RawMessage `json:"modules"`
	}
	if err := json.Unmarshal(raw, &ir); err != nil {
		t.Fatal(err)
	}

	others := 0
	for module := range ir.Modules {
		if served[module] {
			continue
		}
		others++
		prefix := strings.ToUpper(module[:1]) + module[1:]
		for name := range spec.Components.Schemas {
			if strings.HasPrefix(name, prefix) {
				t.Errorf("schema %s belongs to module %s, which another service owns", name, module)
			}
		}
	}
	if others == 0 {
		t.Fatal("every module is served here; the IR should contain at least one owned elsewhere (reports)")
	}
}
