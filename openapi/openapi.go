// Package openapi embeds the OpenAPI specification served at /openapi.yaml.
package openapi

import _ "embed"

//go:embed openapi.yaml
var Spec []byte
