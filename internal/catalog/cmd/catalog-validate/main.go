// catalog-validate checks the usecase catalog embedded in the binary
// and any candidate files given as arguments, printing one line per
// problem (`<source>: <path>: <code>: <message>`) and exiting 1 on
// any. The build step behind `make catalog-validate`; CI runs it on
// every pull request and before every release build. Offline — no
// server, no space, nothing installed.
package main

import (
	"fmt"
	"os"

	"github.com/anyproto/any/internal/catalog"
	"github.com/anyproto/any/internal/server"
)

func main() {
	type source struct {
		name string
		data []byte
	}
	sources := []source{{name: "embedded catalog", data: catalog.Embedded()}}
	for _, path := range os.Args[1:] {
		data, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", path, err)
			os.Exit(2)
		}
		sources = append(sources, source{name: path, data: data})
	}
	failed := false
	for _, s := range sources {
		problems := server.ValidateCatalog(s.data)
		if len(problems) == 0 {
			fmt.Printf("%s: ok\n", s.name)
			continue
		}
		failed = true
		for _, p := range problems {
			fmt.Printf("%s: %s\n", s.name, p)
		}
	}
	if failed {
		os.Exit(1)
	}
}
