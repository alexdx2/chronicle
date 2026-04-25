package registry

import _ "embed"

//go:embed defaults.yaml
var DefaultRegistryYAML []byte

func LoadDefaults() (*Registry, error) {
	return Load(DefaultRegistryYAML)
}
