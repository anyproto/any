// Package server implements the HTTP/JSON API for any-sync-sdk.
//
//	@title			Any API
//	@version		1.0
//	@description	Local HTTP/JSON API wrapping any-sync-sdk. Localhost-only, no auth in v1. A request that names an object (an objectId in the path, body or query string) takes the full id or a short id: 5 to 52 characters of [a-z2-7], read as the part of the id after "bafyrei", with or without that prefix. A short id several objects share answers 409 object.ambiguous_id.
//
//	@servers.url	http://127.0.0.1:2046/v1
package server
