package program

import "github.com/anyproto/any-sync-sdk/handler"

const (
	TypeId      = "program"
	Name        = "Program"
	Description = "guest program: source only — docs live in the source's docstrings"

	DatasetSource = "program_source"

	// Property keys on the program namespace (literal, like a builtin). `name`
	// is the program's identifier (a valid module name), `version` its
	// version tag (e.g. "v1") — distinct from the object's display name
	// (any.name). `any_tool` marks the program as an agent-callable tool and
	// `summary` carries its one-liner (the module docstring's first line);
	// both are DERIVED from the source's shape by deploy — caches of code
	// shape, never authored metadata (anybao ADR-010 §4). Tool discovery
	// gates strictly on `any_tool`; everything richer than the summary is
	// read from the source itself (help()/describe() in the guest kernel).
	PropName    = "name"
	PropVersion = "version"
	PropAnyTool = "any_tool"
	PropSummary = "summary"
)

func NewType() handler.Type {
	return handler.Type{
		Id:          TypeId,
		Name:        Name,
		Description: Description,
		// Declared so the SDK's write-time validation accepts program.name /
		// program.version. Without these, every program create is rejected
		// with property.not_found.
		Properties: []handler.PropertyDecl{
			{Id: PropName, Name: "Name", Kind: handler.PropertyKindString},
			{Id: PropVersion, Name: "Version", Kind: handler.PropertyKindString},
			{Id: PropAnyTool, Name: "Any Tool", Kind: handler.PropertyKindBoolean},
			{Id: PropSummary, Name: "Summary", Kind: handler.PropertyKindString},
		},
		Datasets: []handler.Dataset{
			{Name: DatasetSource, DataVersion: "1", Handler: handler.DefaultHandler{}},
		},
	}
}
