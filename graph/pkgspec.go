package graph

import "strings"

// ParsePackageSpecifier splits an npm import specifier into its package
// identity and subpath (SQ-Contract 1: package identity is not the import
// specifier). Relative specifiers are not packages.
//
//	"@okeep/ui/button" → ("@okeep/ui", "/button")
//	"@okeep/ui"        → ("@okeep/ui", "")
//	"lodash/fp"        → ("lodash", "/fp")
//	"./x", "../x"      → ("", "")
func ParsePackageSpecifier(spec string) (pkg, subpath string) {
	if spec == "" || strings.HasPrefix(spec, ".") || strings.HasPrefix(spec, "/") {
		return "", ""
	}
	parts := strings.Split(spec, "/")
	n := 1
	if strings.HasPrefix(spec, "@") && len(parts) >= 2 {
		n = 2
	}
	pkg = strings.Join(parts[:n], "/")
	if len(parts) > n {
		subpath = "/" + strings.Join(parts[n:], "/")
	}
	return pkg, subpath
}
