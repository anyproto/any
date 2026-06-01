package program

import "github.com/anyproto/any-sync-sdk/handler"

const (
	TypeId      = "program"
	Name        = "Program"
	Description = "JS program with source, description, and method docs"

	DatasetSource      = "program_source"
	DatasetDescription = "program_description"
	DatasetMethods     = "program_methods"
)

func NewType() handler.Type {
	return handler.Type{
		Id:          TypeId,
		Name:        Name,
		Description: Description,
		Datasets: []handler.Dataset{
			{Name: DatasetSource, DataVersion: "1", Handler: handler.DefaultHandler{}},
			{Name: DatasetDescription, DataVersion: "1", Handler: handler.DefaultHandler{}},
			{Name: DatasetMethods, DataVersion: "1", Handler: handler.DefaultHandler{}},
		},
	}
}
