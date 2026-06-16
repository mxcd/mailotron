package render

import (
	"regexp"
	"sort"
)

var varRef = regexp.MustCompile(`\{\{[-\s]*\.([A-Za-z_][A-Za-z0-9_]*)`)

// ExtractVars returns the sorted, unique set of {{.Name}} variables referenced
// in a template.
func ExtractVars(tmpl string) []string {
	seen := map[string]struct{}{}
	for _, m := range varRef.FindAllStringSubmatch(tmpl, -1) {
		seen[m[1]] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func reservedSet() map[string]struct{} {
	s := make(map[string]struct{}, len(Reserved))
	for _, r := range Reserved {
		s[r] = struct{}{}
	}
	return s
}

// UserVars returns referenced template variables excluding renderer-reserved
// names — i.e. the variables a caller must actually supply.
func UserVars(tmpl string) []string {
	res := reservedSet()
	var out []string
	for _, v := range ExtractVars(tmpl) {
		if _, ok := res[v]; !ok {
			out = append(out, v)
		}
	}
	return out
}
