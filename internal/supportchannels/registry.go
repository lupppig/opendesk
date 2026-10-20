package supportchannels

import (
	"fmt"
	"sort"

	"github.com/useopendesk/opendesk/internal/supportchannels/channel"
)

// Registry is the provider plugin registry. It is intentionally independent of
// storage and HTTP routing so an adapter can be tested and added without editing
// the support-channel engine.
type Registry struct{ adapters map[string]channel.Adapter }

func NewRegistry(adapters ...channel.Adapter) (*Registry, error) {
	r := &Registry{adapters: map[string]channel.Adapter{}}
	for _, a := range adapters {
		d := a.Describe()
		if d.Key == "" {
			return nil, fmt.Errorf("support channel provider has no key")
		}
		if _, exists := r.adapters[d.Key]; exists {
			return nil, fmt.Errorf("duplicate support channel provider %q", d.Key)
		}
		r.adapters[d.Key] = a
	}
	return r, nil
}

func (r *Registry) Get(key string) (channel.Adapter, bool) { a, ok := r.adapters[key]; return a, ok }
func (r *Registry) List() []channel.Descriptor {
	out := make([]channel.Descriptor, 0, len(r.adapters))
	for _, a := range r.adapters {
		d := a.Describe()
		_, d.Configures = a.(channel.Configurer)
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
