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
		Handlers: []handler.Registration{
			{Handler: handler.DefaultHandler{DatasetName: DatasetSource, HandlerVersion: 1}, DataVersion: "1"},
			{Handler: handler.DefaultHandler{DatasetName: DatasetDescription, HandlerVersion: 1}, DataVersion: "1"},
			{Handler: handler.DefaultHandler{DatasetName: DatasetMethods, HandlerVersion: 1}, DataVersion: "1"},
		},
	}
}
