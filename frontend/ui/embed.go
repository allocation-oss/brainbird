package ui

import "embed"

//go:embed app.js styles.css index.html favicon.ico assets/branding/*.png
var Files embed.FS
