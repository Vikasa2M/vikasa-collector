package openits

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Identityref leaves travel as plain strings, so a misspelled identity
// compiles, encodes, and stays byte-stable in its golden. Nothing else would
// notice. This test reads every identity the emitter's source builds —
// always as prefix + "name" — and requires each to be declared in that
// module's YANG in the PINNED openits-models release.
func TestEveryEmittedIdentityIsDeclaredUpstream(t *testing.T) {
	out, err := exec.CommandContext(t.Context(), "go", "list", "-m", "-f", "{{.Dir}}", modelsModule).Output()
	if err != nil {
		t.Fatalf("locate %s: %v", modelsModule, err)
	}
	yangDir := filepath.Join(strings.TrimSpace(string(out)), "yang")

	prefixes := map[string]string{
		"scTypes": scTypes, "dmsTypes": dmsTypes, "cctvTypes": cctvTypes,
		"trafficSenTypes": trafficSenTypes, "perceptionTypes": perceptionTypes,
		"rlTypes":      rlTypes,
		"openitsTypes": openitsTypes, "zoneOccTypes": zoneOccTypes, "commHealth": commHealth,
	}
	literal := regexp.MustCompile(`\b(\w+)\s*\+\s*"([a-z0-9-]+)"`)

	sources, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	declared := map[string]map[string]bool{} // module -> identity names
	checked := 0
	for _, src := range sources {
		if strings.HasSuffix(src, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(src)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range literal.FindAllStringSubmatch(string(raw), -1) {
			prefix, ok := prefixes[m[1]]
			if !ok {
				continue // not an identity prefix (e.g. a URL join)
			}
			module := strings.TrimSuffix(prefix, ":")
			if declared[module] == nil {
				declared[module] = identitiesIn(t, filepath.Join(yangDir, module+".yang"))
			}
			if !declared[module][m[2]] {
				t.Errorf("%s: %s%s is not an identity declared in %s.yang of the pinned release", src, prefix, m[2], module)
			}
			checked++
		}
	}
	if checked < 50 {
		t.Fatalf("checked only %d identities; the source pattern no longer matches how identities are built", checked)
	}
}

func identitiesIn(t *testing.T, path string) map[string]bool {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	names := map[string]bool{}
	for _, m := range regexp.MustCompile(`(?m)^\s*identity\s+([a-z0-9-]+)`).FindAllStringSubmatch(string(raw), -1) {
		names[m[1]] = true
	}
	return names
}

// ce-dataschema names the schema revision a payload conforms to. Older
// revisions stay in the registry forever, so a stale one still resolves —
// it just describes a payload shape we no longer send. Every entry must name
// its module's NEWEST revision in the pinned release's schema registry, so a
// pin bump that forgets to move them fails here.
func TestDataSchemasNameThePinnedRevision(t *testing.T) {
	out, err := exec.CommandContext(t.Context(), "go", "list", "-m", "-f", "{{.Dir}}", modelsModule).Output()
	if err != nil {
		t.Fatalf("locate %s: %v", modelsModule, err)
	}
	registry := filepath.Join(strings.TrimSpace(string(out)), "schema-registry")
	for ceType, url := range dataSchemaFor {
		rest, ok := strings.CutPrefix(url, registryBase)
		module, rev, ok2 := strings.Cut(strings.TrimSuffix(rest, "/"), "/")
		if !ok || !ok2 {
			t.Errorf("%s: dataschema %q is not %s<module>/<revision>/", ceType, url, registryBase)
			continue
		}
		entries, err := os.ReadDir(filepath.Join(registry, module))
		if err != nil {
			t.Errorf("%s: module %s is not in the pinned schema registry: %v", ceType, module, err)
			continue
		}
		newest := ""
		for _, e := range entries {
			if e.IsDir() && e.Name() > newest {
				newest = e.Name() // revisions are YYYY-MM-DD, so lexical is chronological
			}
		}
		if rev != newest {
			t.Errorf("%s: dataschema names %s revision %s; the pinned release's newest is %s", ceType, module, rev, newest)
		}
	}
}
