package catalog

import (
	"slices"
	"strings"
	"testing"

	"github.com/anyproto/any/internal/api"
)

var knownTypes = Options{
	KnownTypeIds: []string{"page", "miniapp", "bin", "dataview"},
	RootTypeIds:  []string{"page", "dataview"},
}

// TestCatalog_EmbeddedLoads is the build-time check: the catalog that
// ships in the binary passes the pure validation.
func TestCatalog_EmbeddedLoads(t *testing.T) {
	cat, problems := Load(Embedded(), knownTypes)
	if len(problems) > 0 {
		t.Fatalf("embedded catalog: %v", problems)
	}
	for _, id := range []string{"wiki", "collections", "journal", "meetings", "general-chat", "people", "contact", "contacts", "crm"} {
		if _, ok := cat.Get(id); !ok {
			t.Errorf("usecase %s missing", id)
		}
	}
}

// Collections is the one navigation-only app: a feature switch with no
// type of its own. An app that brings a type declares it here — the
// catalog is where every client resolves it from.
func TestCatalog_CollectionsIsNavigationOnly(t *testing.T) {
	cat, problems := Load(Embedded(), knownTypes)
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	u, ok := cat.Get("collections")
	if !ok || len(u.Requires) != 0 || len(u.Bundles) != 1 {
		t.Fatalf("collections usecase: %+v", u)
	}
	b := u.Bundles[0]
	if b.Id != "system:collections/v1" || b.Miniapp == nil || len(b.Miniapp) != 0 ||
		b.Type != nil || len(b.Parts) != 0 || b.Derived || b.Hidden {
		t.Fatalf("navigation-only bundle: %+v", b)
	}
	// A root that declares nothing still has a type: the plain document.
	if b.RootType != "page" {
		t.Fatalf("navigation-only root type = %q", b.RootType)
	}
}

// Every sidebar app whose client mints definitions has them in the
// catalog: one bundle, the handles clients resolve by. A wiki page keeps
// its own type, so the wiki app declares a COLLECTION its pages are
// filed under.
func TestCatalog_SidebarAppsDeclareTheirTypes(t *testing.T) {
	cat, problems := Load(Embedded(), knownTypes)
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	// The app root and the definition it brings, per usecase.
	defs := map[string]struct {
		xKey       string
		collection bool
		sidebar    string
	}{
		"journal":  {xKey: "journal", collection: true, sidebar: "system:journal/v2"},
		"meetings": {xKey: "meeting", sidebar: "system:meetings/v1"},
		"wiki":     {xKey: "wiki", collection: true, sidebar: "system:wiki/v1"},
	}
	for id, def := range defs {
		t.Run(id, func(t *testing.T) {
			u, ok := cat.Get(id)
			if !ok {
				t.Fatalf("usecase %s missing", id)
			}
			var declaring, sidebar *api.CatalogBundle
			for i := range u.Bundles {
				b := &u.Bundles[i]
				if cat.Superseded(b.Id) {
					continue // kept where installed, never what a new space gets
				}
				if def.collection {
					if b.Collection != nil && b.Collection.XKey == def.xKey {
						declaring = b
					}
				} else if b.Type != nil && b.Type.XKey == def.xKey {
					declaring = b
				}
				if b.Miniapp != nil {
					sidebar = b
				}
			}
			if declaring == nil || sidebar == nil {
				t.Fatalf("%s declares no %s or no sidebar root: %+v", id, def.xKey, u.Bundles)
			}
			if sidebar.Id != def.sidebar {
				t.Fatalf("%s sidebar root: %s", id, sidebar.Id)
			}
			// A collection is columns only — no parts, no type beside it.
			if def.collection && (declaring.Type != nil || len(declaring.Parts) > 0) {
				t.Fatalf("%s collection root carries a type or parts: %+v", id, declaring)
			}
		})
	}
	// Journal's entry is a page filed under a collection that carries its
	// date; a meeting is an object with three surfaces.
	journal, _ := cat.Get("journal")
	if journal.Bundles[0].Collection == nil {
		t.Fatalf("journal's first bundle is the collection: %+v", journal.Bundles[0])
	}
	if props := journal.Bundles[0].Collection.Properties; len(props) != 1 || props[0].XKey != "date" ||
		props[0].Kind != api.PropertyKindDatetime {
		t.Fatalf("journal properties: %+v", props)
	}
	meetings, _ := cat.Get("meetings")
	parts := meetings.Bundles[0].Parts
	if len(parts) != 3 {
		t.Fatalf("a meeting has notes, summary and transcript: %+v", parts)
	}
	surfaces := map[string]api.DatasetDraftRequest{}
	for _, p := range parts {
		if len(p.Datasets) != 1 {
			t.Fatalf("part %s: %+v", p.Key, p.Datasets)
		}
		surfaces[p.Key] = p.Datasets[0]
	}
	// The notes are the COMMON editor (shared with page); the summary is a
	// second editor of its own; the transcript is upserted by segment id.
	if surfaces["notes"].Module != "editor" || !surfaces["notes"].Shared {
		t.Fatalf("notes: %+v", surfaces["notes"])
	}
	if surfaces["summary"].Module != "editor" || surfaces["summary"].Shared ||
		surfaces["summary"].Key != "summary" {
		t.Fatalf("summary: %+v", surfaces["summary"])
	}
	if surfaces["transcript"].Key != "transcript" || surfaces["transcript"].IdRule != "user" {
		t.Fatalf("transcript: %+v", surfaces["transcript"])
	}
}

// A relationship facet is a collection: an identity keeps its own type
// and its profile layout and is filed under the facets it plays.
func TestCatalog_RoleFacetsAreCollections(t *testing.T) {
	cat, problems := Load(Embedded(), knownTypes)
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	for _, id := range []string{"contact", "investor", "customer", "partner", "vendor", "cofounder", "candidate"} {
		u, ok := cat.Get(id)
		if !ok {
			t.Fatalf("usecase %s missing", id)
		}
		b := u.Bundles[0]
		if b.Collection == nil || b.Collection.XKey != id || b.Type != nil || len(b.Parts) > 0 {
			t.Errorf("%s facet: %+v", id, b)
		}
	}
	// The contacts app root hosts its own layouts records — a definition
	// implements itself, so nothing declares that.
	contacts, _ := cat.Get("contacts")
	root := contacts.Bundles[0]
	if root.Type != nil || root.Collection != nil || len(root.Parts) != 1 {
		t.Fatalf("contacts root: %+v", root)
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
          layout: { type: profile }
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
          layout: { type: profile }
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

// bareRoot strips the crm root's declaration, leaving a sidebar entry
// that declares nothing — the shape that needs a rootType.
func bareRoot(s string) string {
	return strings.Replace(s, `        hidden: true
        parts:
          - key: settings
            datasets:
              - key: settings
                idRule: user
                fields: [ { key: pipeline, kind: string, mutableBy: any } ]
`, "", 1)
}

func TestCatalog_BaseIsValid(t *testing.T) {
	if _, problems := Load([]byte(base), knownTypes); len(problems) > 0 {
		t.Fatalf("base: %v", problems)
	}
}

// A root that declares nothing mints an ordinary object, so it names
// the type that object gets.
func TestCatalog_BareRootWithRootTypeIsValid(t *testing.T) {
	src := strings.Replace(bareRoot(base), "        miniapp: {}\n",
		"        miniapp: {}\n        rootType: page\n", 1)
	if _, problems := Load([]byte(src), knownTypes); len(problems) > 0 {
		t.Fatalf("bare root with rootType: %v", problems)
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
			name:   "bare root without a rootType",
			mutate: bareRoot,
			code:   CodeMissing, path: "usecases[2].bundles[0].rootType", contains: "page for a plain document",
		},
		{
			name: "rootType is not a registered type",
			mutate: func(s string) string {
				return strings.Replace(bareRoot(s), "        miniapp: {}\n",
					"        miniapp: {}\n        rootType: nope\n", 1)
			},
			code: CodeBadField, path: "usecases[2].bundles[0].rootType", contains: "not a registered type",
		},
		{
			name: "rootType next to a declaration",
			mutate: func(s string) string {
				return strings.Replace(s, "        type:\n          xKey: contact\n",
					"        rootType: page\n        type:\n          xKey: contact\n", 1)
			},
			code: CodeBadField, path: "usecases[0].bundles[0].rootType", contains: "carries its marker",
		},
		{
			name: "mutableBy author without a creator stamp",
			mutate: func(s string) string {
				return strings.Replace(s, "{ key: pipeline, kind: string, mutableBy: any }",
					"{ key: pipeline, kind: string, mutableBy: author }", 1)
			},
			code: CodeBadField, path: "usecases[2].bundles[0].parts[0].datasets[0].fields",
			contains: "stamp creator",
		},
		{
			name: "search mapping names an undeclared field",
			mutate: func(s string) string {
				return strings.Replace(s, "                idRule: user\n",
					"                idRule: user\n                search: { title: pipelien }\n", 1)
			},
			code: CodeBadField, path: "usecases[2].bundles[0].parts[0].datasets[0].search.title",
			contains: "no field pipelien",
		},
		{
			name: "type and collection on one root",
			mutate: func(s string) string {
				return strings.Replace(s, "        type:\n          xKey: contact\n",
					"        collection: { xKey: contact_facet }\n        type:\n          xKey: contact\n", 1)
			},
			code: CodeBadField, path: "usecases[0].bundles[0].collection", contains: "not both",
		},
		{
			name: "collection with parts",
			mutate: func(s string) string {
				return strings.Replace(s, "        miniapp: {}\n",
					"        miniapp: {}\n        collection: { xKey: pipeline }\n", 1)
			},
			code: CodeBadField, path: "usecases[2].bundles[0].parts", contains: "declares no parts",
		},
		{
			name: "collection xKey collides with a type xKey",
			mutate: func(s string) string {
				return strings.Replace(s, "        type:\n          xKey: company\n          layout: { type: profile }\n",
					"        collection:\n          xKey: contact\n", 1)
			},
			code: CodeDuplicate, path: "usecases[1].bundles[0].collection.xKey", contains: "system:contact/v1",
		},
		{
			name: "weight is gone from a type",
			mutate: func(s string) string {
				return strings.Replace(s, "          xKey: company\n", "          xKey: company\n          weight: 5\n", 1)
			},
			code: CodeUnknownField, path: "usecases[1].bundles[0].type.weight",
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
	cases = append(cases, []struct {
		name     string
		mutate   func(string) string
		code     string
		path     string
		contains string
	}{
		{
			name: "a type with no layout and no part is a collection",
			mutate: func(s string) string {
				return strings.Replace(s, "          xKey: company\n          layout: { type: profile }\n", "          xKey: company\n", 1)
			},
			code: CodeBadField, path: "usecases[1].bundles[0].type", contains: "is a collection",
		},
		{
			name:   "supersedes names a bundle outside the usecase",
			mutate: func(s string) string { return withSuperseded(s, "system:contact/v1") },
			code:   CodeBrokenLink, path: "usecases[1].bundles[0].supersedes[0]", contains: "usecase company",
		},
		{
			name:   "supersedes itself",
			mutate: func(s string) string { return withSuperseded(s, "system:company/v1") },
			code:   CodeCycle, path: "usecases[1].bundles[0].supersedes[0]",
		},
		{
			name: "superseded is derived, not declared",
			mutate: func(s string) string {
				return strings.Replace(s, "        name: Company\n", "        name: Company\n        superseded: true\n", 1)
			},
			code: CodeBadField, path: "usecases[1].bundles[0].superseded", contains: "derived",
		},
		{
			name: "two bundles share a handle without superseding",
			mutate: func(s string) string {
				return strings.Replace(withOldCompany(s), "        supersedes: [ system:firm/v1 ]\n", "", 1)
			},
			code: CodeDuplicate, path: "usecases[1].bundles[1].type.xKey",
		},
		{
			name: "collection defaultType names a collection",
			mutate: func(s string) string {
				return strings.Replace(asCollection(s), "          xKey: company\n", "          xKey: company\n          meta: { defaultType: company }\n", 1)
			},
			code: CodeBrokenLink, path: "usecases[1].bundles[0].collection.meta.defaultType", contains: "is a collection",
		},
		{
			name: "collection defaultType names no type",
			mutate: func(s string) string {
				return strings.Replace(asCollection(s), "          xKey: company\n", "          xKey: company\n          meta: { defaultType: nosuch }\n", 1)
			},
			code: CodeBrokenLink, path: "usecases[1].bundles[0].collection.meta.defaultType", contains: "no type",
		},
		{
			name: "collection meta key is not single-level",
			mutate: func(s string) string {
				return strings.Replace(asCollection(s), "          xKey: company\n", "          xKey: company\n          meta: { a.b: x }\n", 1)
			},
			code: CodeBadField, path: "usecases[1].bundles[0].collection.meta.a.b", contains: "single-level",
		},
		{
			name: "collection meta value is not a scalar",
			mutate: func(s string) string {
				return strings.Replace(asCollection(s), "          xKey: company\n", "          xKey: company\n          meta: { defaultType: [ page ] }\n", 1)
			},
			code: CodeBadField, path: "usecases[1].bundles[0].collection.meta.defaultType", contains: "strings, booleans or numbers",
		},
	}...)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := tc.mutate(base)
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

// withSuperseded makes the company bundle supersede `old`.
func withSuperseded(s, old string) string {
	return strings.Replace(s, "        name: Company\n", "        name: Company\n        supersedes: [ "+old+" ]\n", 1)
}

// withOldCompany adds a bundle the company bundle supersedes, sharing
// its handle — the shape a definition takes when it changes kind.
func withOldCompany(s string) string {
	s = withSuperseded(s, "system:firm/v1")
	return strings.Replace(s, "  - id: crm\n", `      - id: system:firm/v1
        name: Company
        type:
          xKey: company
  - id: crm
`, 1)
}

// asCollection turns the company type into a collection.
func asCollection(s string) string {
	return strings.Replace(s, "        type:\n          xKey: company\n          layout: { type: profile }\n",
		"        collection:\n          xKey: company\n", 1)
}

// A bundle and the one it supersedes share a handle, and the superseded
// one is exempt from the format rule: it is never installed anew.
func TestCatalog_SupersededSharesItsHandle(t *testing.T) {
	cat, problems := Load([]byte(withOldCompany(base)), knownTypes)
	if len(problems) > 0 {
		t.Fatalf("superseding pair: %v", problems)
	}
	if !cat.Superseded("system:firm/v1") || cat.Superseded("system:company/v1") {
		t.Fatalf("superseded set: firm=%v company=%v", cat.Superseded("system:firm/v1"), cat.Superseded("system:company/v1"))
	}
}

// The `supersedes` edges form groups decided as a whole, and the
// listing marks every superseded bundle.
func TestCatalog_SupersedeGroups(t *testing.T) {
	cat, problems := Load(Embedded(), knownTypes)
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	g := cat.SupersedeGroup("system:profile/v1")
	if g == nil {
		t.Fatal("profile has no group")
	}
	if !slices.Equal(g.Old, []string{"system:person/v1", "system:organization/v1"}) ||
		!slices.Equal(g.New, []string{"system:profile/v1", "system:person/v2", "system:organization/v2"}) {
		t.Fatalf("people group: old=%v new=%v", g.Old, g.New)
	}
	for _, id := range append(g.Old, g.New...) {
		if cat.SupersedeGroup(id) != g {
			t.Fatalf("%s is not in the people group", id)
		}
	}
	if cat.SupersedeGroup("system:contact/v1") != nil {
		t.Fatal("contact is in a group")
	}
	if j := cat.SupersedeGroup("system:journal/v2"); j == nil ||
		!slices.Equal(j.Old, []string{"system:journal/v1"}) || !slices.Equal(j.New, []string{"system:journal/v2"}) {
		t.Fatalf("journal group: %+v", j)
	}
	have := map[string]bool{"system:organization/v1": true}
	if !g.OldKept(have) || g.NewAny(have) {
		t.Fatal("one old bundle keeps the group on the old shape")
	}
	people, _ := cat.Get("people")
	flags := map[string]bool{}
	for _, b := range people.Bundles {
		flags[b.Id] = b.Superseded
	}
	if !flags["system:person/v1"] || !flags["system:organization/v1"] || flags["system:profile/v1"] || flags["system:person/v2"] {
		t.Fatalf("superseded flags: %v", flags)
	}
}

// Two bundles superseding the same one may not both claim its handle:
// a new space would install both. Declared new, old, new so the check
// has to look past the last holder.
func TestCatalog_HandleSharedByTwoSupersedersIsRefused(t *testing.T) {
	src := `
usecases:
  - id: things
    name: Things
    bundles:
      - id: system:thing-card/v1
        name: Card
        supersedes: [ system:thing/v1 ]
        type: { xKey: thing, layout: { type: profile } }
      - id: system:thing/v1
        name: Thing
        type: { xKey: thing }
      - id: system:thing/v2
        name: Things
        supersedes: [ system:thing/v1 ]
        collection: { xKey: thing }
`
	_, ps := Load([]byte(src), knownTypes)
	for _, p := range ps {
		if p.Code == CodeDuplicate && p.Path == "usecases[0].bundles[2].collection.xKey" {
			return
		}
	}
	t.Fatalf("no duplicate-handle problem; got:\n%v", ps)
}

// A chain would make "which one does a space keep" ambiguous.
func TestCatalog_SupersedesIsOneStep(t *testing.T) {
	src := strings.Replace(withOldCompany(base), "      - id: system:firm/v1\n        name: Company\n",
		"      - id: system:firm/v1\n        name: Company\n        supersedes: [ system:business/v1 ]\n", 1)
	src = strings.Replace(src, "  - id: crm\n", `      - id: system:business/v1
        name: Company
        type:
          xKey: company
  - id: crm
`, 1)
	_, ps := Load([]byte(src), knownTypes)
	for _, p := range ps {
		if p.Code == CodeBadField && strings.Contains(p.Message, "no chains") {
			return
		}
	}
	t.Fatalf("no chain problem; got:\n%v", ps)
}

// The shipped catalog follows the rule it enforces: every type a new
// space receives is a format, and what only adds columns is a collection
// whose rows are profiles or pages.
func TestCatalog_TypesAreFormats(t *testing.T) {
	cat, problems := Load(Embedded(), knownTypes)
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	collections := map[string]string{} // xKey -> meta.defaultType
	types := map[string]*api.CatalogBundle{}
	for ui := range cat.Usecases {
		for bi := range cat.Usecases[ui].Bundles {
			b := &cat.Usecases[ui].Bundles[bi]
			if cat.Superseded(b.Id) {
				continue
			}
			if b.Collection != nil {
				dt, _ := b.Collection.Meta["defaultType"].(string)
				collections[b.Collection.XKey] = dt
			}
			if b.Type != nil {
				types[b.Type.XKey] = b
				if !hasLayout(b.Type.Layout) && len(b.Parts) == 0 {
					t.Fatalf("type %s brings neither a layout nor a part", b.Type.XKey)
				}
			}
		}
	}
	for xKey, defaultType := range map[string]string{
		"person": "profile", "organization": "profile", "contact": "profile", "investor": "profile",
		"deal": "", "project": "", "area": "", "journal": "",
	} {
		got, ok := collections[xKey]
		if !ok {
			t.Fatalf("%s is not a collection in what a new space receives", xKey)
		}
		if got != defaultType {
			t.Fatalf("%s default row type: got %q, want %q", xKey, got, defaultType)
		}
	}
	profile := types["profile"]
	if profile == nil || len(profile.Type.Properties) != 0 || len(profile.Parts) != 1 {
		t.Fatalf("profile is a format with a body and no columns: %+v", profile)
	}
	for _, xKey := range []string{"person", "organization", "deal", "project", "area", "journal"} {
		if types[xKey] != nil {
			t.Fatalf("%s is still a type a new space receives", xKey)
		}
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
