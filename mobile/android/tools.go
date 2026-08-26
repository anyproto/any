//go:build tools

// This file pins golang.org/x/mobile/bind in go.mod so that `gomobile bind`
// (which is `gobind` under the hood) can import it when generating the AAR.
// Without this, `go mod tidy` would drop the dep and the next gomobile build
// fails with "no Go package in golang.org/x/mobile/bind".

package mobile

import _ "golang.org/x/mobile/bind"
