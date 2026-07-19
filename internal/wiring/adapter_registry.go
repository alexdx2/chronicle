package wiring

import "sort"

var adapters = map[string]Adapter{}

func RegisterAdapter(a Adapter) { adapters[a.ID()] = a }

func AdapterByID(id string) (Adapter, bool) {
	a, ok := adapters[id]
	return a, ok
}

func AdapterIDs() []string {
	ids := make([]string, 0, len(adapters))
	for id := range adapters {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// resetAdapters is a test hook.
func resetAdapters() { adapters = map[string]Adapter{} }
