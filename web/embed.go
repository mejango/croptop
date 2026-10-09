// Package web embeds the console UI.
package web

import "embed"

//go:embed index.html app.js style.css shop.html shop.js shop.css shop-logo.png shop-connect.html shop-connect.js shop-setup.html shop-setup.js
var FS embed.FS
