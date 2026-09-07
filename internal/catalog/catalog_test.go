package catalog

import (
	"strings"
	"testing"
)

var knownTypes = Options{KnownTypeIds: []string{"page", "miniapp", "bin", "dataview"}}

// TestCatalog_EmbeddedLoads is the build-time check: the catalog that
// ships in the binary passes the pure validation.
func TestCatalog_EmbeddedLoads(t *testing.T) {
	cat, problems := Load(Embedded(), knownTypes)
	if len(problems) > 0 {
		t.Fatalf("embedded catalog: %v", problems)
	}
	for _, id := range []string{"wiki", "collections", "general-chat", "people", "contact", "contacts", "crm"} {
		if _, ok := cat.Get(id); !ok {
			t.Errorf("usecase %s missing", id)
		}
	}
}

// base is a small valid catalog the fixtures below mutate.
const base = `
usecases:
  - id: contact
    name: Contact
    bundles:
      - id: system:contact/v1
        name: Contact
        type:
          xKey: contact
          weight: 10
          properties:
            - { xKey: email, name: Email, kind: string, xFormat: { type: email } }
  - id: company
    name: Company
    requires: [ contact ]
    bundles:
      - id: system:company/v1
        name: Company
        type:
          xKey: company
          properties:
            - { xKey: contacts, name: Contacts, kind: array,
                xFormat: { type: relation, relation: { targetTypes: [ contact ] }, config: { multiple: true } } }
  - id: crm
    name: CRM
    requires: [ company ]
    bundles:
      - id: system:crm/v1
        name: CRM
        miniapp: {}
        hidden: true
        parts:
          - key: settings
            datasets:
              - key: settings
                idRule: user
                fields: [ { key: pipeline, kind: string, mutableBy: any } ]
`

func TestCatalog_BaseIsValid(t *testing.T) {
	if _, problems := Load([]byte(base), knownTypes); len(problems) > 0 {
		t.Fatalf("base: %v", problems)
	}
}

func TestCatalog_Order(t *testing.T) {
	cat, problems := Load([]byte(base), knownTypes)
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	order, ok := cat.Order("crm")
	if !ok {
		t.Fatal("crm unknown")
	}
	var ids []string
	for _, u := range order {
		ids = append(ids, u.Id)
	}
	if got := strings.Join(ids, ","); got != "contact,company,crm" {
		t.Fatalf("order = %s", got)
	}
	if _, ok := cat.Order("nope"); ok {
		t.Fatal("unknown usecase ordered")
	}
	alone, _ := cat.Order("contact")
	if len(alone) != 1 || alone[0].Id != "contact" {
		t.Fatalf("standalone order = %+v", alone)
	}
}

// TestCatalog_Problems runs one fixture per problem code and checks
// the code AND the path the finding is reported at.
func TestCatalog_Problems(t *testing.T) {
	cases := []struct {
		name     string
		mutate   func(string) string
		code     string
		path     string
		contains string
	}{
		{
			name: "unknown key",
			mutate: func(s string) string {
				return strings.Replace(s, "    name: Contact\n", "    name: Contact\n    colour: red\n", 1)
			},
			code: CodeUnknownField, path: "usecases[0].colour",
		},
		{
			name:   "usecase id not a slug",
			mutate: func(s string) string { return strings.Replace(s, "- id: contact\n", "- id: Contact_1\n", 1) },
			code:   CodeBadId, path: "usecases[0].id",
		},
		{
			name:   "duplicate usecase id",
			mutate: func(s string) string { return strings.Replace(s, "- id: company\n", "- id: contact\n", 1) },
			code:   CodeDuplicate, path: "usecases[1].id",
		},
		{
			name:   "bundle id outside system:",
			mutate: func(s string) string { return strings.Replace(s, "system:contact/v1", "contact/v1", 1) },
			code:   CodeBadId, path: "usecases[0].bundles[0].id", contains: BundleIdPattern,
		},
		{
			name:   "duplicate bundle id",
			mutate: func(s string) string { return strings.Replace(s, "system:company/v1", "system:contact/v1", 1) },
			code:   CodeDuplicate, path: "usecases[1].bundles[0].id", contains: "usecase contact",
		},
		{
			name:   "duplicate type xKey",
			mutate: func(s string) string { return strings.Replace(s, "xKey: company\n", "xKey: contact\n", 1) },
			code:   CodeDuplicate, path: "usecases[1].bundles[0].type.xKey",
		},
		{
			name:   "xKey is a built-in id",
			mutate: func(s string) string { return strings.Replace(s, "xKey: company\n", "xKey: page\n", 1) },
			code:   CodeDuplicate, path: "usecases[1].bundles[0].type.xKey", contains: "built-in",
		},
		{
			name:   "property without xKey",
			mutate: func(s string) string { return strings.Replace(s, "{ xKey: email, ", "{ ", 1) },
			code:   CodeMissing, path: "usecases[0].bundles[0].type.properties[0].xKey",
		},
		{
			name:   "unknown requires",
			mutate: func(s string) string { return strings.Replace(s, "requires: [ contact ]", "requires: [ contacts ]", 1) },
			code:   CodeUnknownUsecase, path: "usecases[1].requires[0]",
		},
		{
			name:   "self require",
			mutate: func(s string) string { return strings.Replace(s, "requires: [ contact ]", "requires: [ company ]", 1) },
			code:   CodeCycle, path: "usecases[1].requires[0]",
		},
		{
			name: "cycle",
			mutate: func(s string) string {
				return strings.Replace(s, "    name: Contact\n    bundles:", "    name: Contact\n    requires: [ crm ]\n    bundles:", 1)
			},
			code:     CodeCycle,
			contains: "contact → crm → company → contact",
		},
		{
			name:   "relation target outside the requires path",
			mutate: func(s string) string { return strings.Replace(s, "    requires: [ contact ]\n", "", 1) },
			code:   CodeBrokenLink, path: "usecases[1].bundles[0].type.properties[0].xFormat.relation.targetTypes[0]",
			contains: "declared by usecase contact",
		},
		{
			name: "relation target nowhere",
			mutate: func(s string) string {
				return strings.Replace(s, "targetTypes: [ contact ]", "targetTypes: [ deal ]", 1)
			},
			code:     CodeBrokenLink,
			contains: "no such type",
		},
		{
			name: "miniapp bundle mismatch",
			mutate: func(s string) string {
				return strings.Replace(s, "miniapp: {}", "miniapp: { bundle: system:other/v1 }", 1)
			},
			code: CodeBadMiniapp, path: "usecases[2].bundles[0].miniapp.bundle",
		},
		{
			name: "bundle declares nothing",
			mutate: func(s string) string {
				return strings.Replace(s, "        miniapp: {}\n        hidden: true\n        parts:\n          - key: settings\n            datasets:\n              - key: settings\n                idRule: user\n                fields: [ { key: pipeline, kind: string, mutableBy: any } ]\n", "", 1)
			},
			code: CodeMissing, path: "usecases[2].bundles[0]",
		},
		{
			name: "hidden without a declaration",
			mutate: func(s string) string {
				return strings.Replace(s, "        hidden: true\n        parts:\n          - key: settings\n            datasets:\n              - key: settings\n                idRule: user\n                fields: [ { key: pipeline, kind: string, mutableBy: any } ]\n", "        hidden: true\n", 1)
			},
			code: CodeBadField, path: "usecases[2].bundles[0].hidden",
		},
		{
			name: "selfTyped without a declaration",
			mutate: func(s string) string {
				return strings.Replace(s, "        hidden: true\n        parts:\n          - key: settings\n            datasets:\n              - key: settings\n                idRule: user\n                fields: [ { key: pipeline, kind: string, mutableBy: any } ]\n", "        selfTyped: true\n", 1)
			},
			code: CodeBadField, path: "usecases[2].bundles[0].selfTyped",
		},
		{
			name: "weight on a hidden type",
			mutate: func(s string) string {
				return strings.Replace(s, "          xKey: company\n", "          xKey: company\n          weight: 5\n", 1) + ""
			},
			code: CodeBadField, path: "usecases[1].bundles[0].type.weight",
			// hidden must be set on that bundle for the rule to fire
		},
		{
			name: "chat dataset not shared",
			mutate: func(s string) string {
				return strings.Replace(s, "- key: settings\n                idRule: user\n                fields: [ { key: pipeline, kind: string, mutableBy: any } ]", "- { module: chat }", 1)
			},
			code: CodeBadField, path: "usecases[2].bundles[0].parts[0].datasets[0]", contains: "shared only",
		},
		{
			name: "fields on an editor dataset",
			mutate: func(s string) string {
				return strings.Replace(s, "- key: settings\n                idRule: user\n                fields:", "- module: editor\n                shared: true\n                fields:", 1)
			},
			code: CodeBadField, path: "usecases[2].bundles[0].parts[0].datasets[0].fields",
		},
		{
			name: "relation filter",
			mutate: func(s string) string {
				return strings.Replace(s, "relation: { targetTypes: [ contact ] }", "relation: { targetTypes: [ contact ], filter: '{}' }", 1)
			},
			code: CodeBadField, path: "usecases[1].bundles[0].type.properties[0].xFormat.relation.filter",
		},
		{
			name:   "bad yaml",
			mutate: func(s string) string { return s + "\n  - id: [broken\n" },
			code:   CodeBadYAML,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := tc.mutate(base)
			if tc.name == "weight on a hidden type" {
				src = strings.Replace(src, "      - id: system:company/v1\n        name: Company\n", "      - id: system:company/v1\n        name: Company\n        hidden: true\n", 1)
			}
			_, ps := Load([]byte(src), knownTypes)
			if len(ps) == 0 {
				t.Fatalf("expected problems")
			}
			for _, p := range ps {
				if p.Code != tc.code {
					continue
				}
				if tc.path != "" && p.Path != tc.path {
					continue
				}
				if tc.contains != "" && !strings.Contains(p.Message, tc.contains) {
					continue
				}
				return
			}
			t.Fatalf("no problem %s at %q containing %q; got:\n%v", tc.code, tc.path, tc.contains, ps)
		})
	}
}

// TestCatalog_ReportsAllProblems pins that validation does not stop
// at the first finding.
func TestCatalog_ReportsAllProblems(t *testing.T) {
	src := strings.Replace(base, "system:contact/v1", "contact/v1", 1)
	src = strings.Replace(src, "requires: [ company ]", "requires: [ nothing ]", 1)
	src = strings.Replace(src, "    name: Company\n    requires", "    name: Company\n    extra: 1\n    requires", 1)
	_, ps := Load([]byte(src), knownTypes)
	// The unknown key is reported alone (decoding stops there); the
	// structural pass reports the rest together.
	if len(ps) != 1 || ps[0].Code != CodeUnknownField {
		t.Fatalf("unknown key first: %v", ps)
	}
	src = strings.Replace(src, "    extra: 1\n", "", 1)
	if _, ps = Load([]byte(src), knownTypes); len(ps) < 2 {
		t.Fatalf("expected several problems, got %v", ps)
	}
}
