package web

import "embed"

// Assets is the production React application. Run npm run build before go build.
//
//go:embed all:dist
var Assets embed.FS
