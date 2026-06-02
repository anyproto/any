package program

import "github.com/anyproto/any-sync-sdk/handler"

const (
	TypeId      = "program"
	Name        = "Program"
	Description = "JS program with source, description, and method docs"

	DatasetSource      = "program_source"
	DatasetDescription = "program_description"
	DatasetMethods     = "program_methods"

	// Property keys on the program namespace (literal, like a builtin). `name`
	// is the program's identifier (a valid JS module name), `version` its
	// version tag (e.g. "v1") — distinct from the object's display name
	// (any.name). Source + docs live in the datasets above.
	PropName    = "name"
	PropVersion = "version"
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
		},
		Datasets: []handler.Dataset{
			{Name: DatasetSource, DataVersion: "1", Handler: handler.DefaultHandler{}},
			{Name: DatasetDescription, DataVersion: "1", Handler: handler.DefaultHandler{}},
			{Name: DatasetMethods, DataVersion: "1", Handler: handler.DefaultHandler{}},
		},
	}
}
