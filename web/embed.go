// Package web embeds the single-page status dashboard and its PWA assets.
package web

import "embed"

// FS holds index.html, manifest.webmanifest and the home-screen icons. The
// page uses relative URLs so it works behind Home Assistant ingress as well
// as on a bare port.
//
//go:embed index.html manifest.webmanifest icon-192.png icon-512.png
var FS embed.FS
