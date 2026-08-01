package handler

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	swaggerFiles "github.com/swaggo/files"
)

const swaggerUIHTML = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <title>KelolaKelas Platform API Documentation</title>
  <link rel="stylesheet" type="text/css" href="./swagger-ui.css" />
  <link rel="icon" type="image/png" href="./favicon-32x32.png" sizes="32x32" />
  <style>
    html { box-sizing: border-box; overflow: -moz-scrollbars-vertical; overflow-y: scroll; }
    *, *:before, *:after { box-sizing: inherit; }
    body { margin:0; background: #fafafa; }
  </style>
</head>
<body>
<div id="swagger-ui"></div>
<script src="./swagger-ui-bundle.js" charset="UTF-8"> </script>
<script src="./swagger-ui-standalone-preset.js" charset="UTF-8"> </script>
<script>
window.onload = function() {
  const ui = SwaggerUIBundle({
    urls: [
      { url: "/identity/swagger/doc.json", name: "Identity Service" },
      { url: "/academic/swagger/doc.json", name: "Academic Service" },
      { url: "/billing/swagger/doc.json", name: "Billing Service" }
    ],
    "urls.primaryName": "Identity Service",
    dom_id: '#swagger-ui',
    deepLinking: true,
    presets: [
      SwaggerUIBundle.presets.apis,
      SwaggerUIStandalonePreset
    ],
    plugins: [
      SwaggerUIBundle.plugins.DownloadUrl
    ],
    layout: "StandaloneLayout"
  });
  window.ui = ui;
};
</script>
</body>
</html>`

func SwaggerUIHandler() gin.HandlerFunc {
	fileServer := http.FileServer(swaggerFiles.HTTP)

	return func(c *gin.Context) {
		path := c.Param("any")
		if path == "" || path == "/" || path == "/index.html" {
			c.Header("Content-Type", "text/html; charset=utf-8")
			c.String(http.StatusOK, swaggerUIHTML)
			return
		}

		c.Request.URL.Path = strings.TrimPrefix(c.Request.URL.Path, "/swagger")
		fileServer.ServeHTTP(c.Writer, c.Request)
	}
}
