// Package web embeds the single-page status dashboard.
package web

import "embed"

// FS holds index.html. The page uses relative URLs so it works behind
// Home Assistant ingress as well as on a bare port.
//
//go:embed index.html
var FS embed.FS
