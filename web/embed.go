package web

import "embed"

//go:embed all:templates
var Templates embed.FS

//go:embed all:static/dist
var Static embed.FS
