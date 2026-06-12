package index

// Registry holds the set of chunkers known to the process. Phase 1 has
// no consumer — it's constructed at boot and stored on server deps so
// the phase-2 indexer can look up "which chunkers feed this dataset"
// without re-wiring. Read-only after construction; safe for concurrent
// reads.
type Registry struct {
	chunkers []Chunker
}

// NewRegistry builds a registry over the given chunkers, in order.
func NewRegistry(chunkers ...Chunker) *Registry {
	return &Registry{chunkers: chunkers}
}

// All returns every registered chunker, in registration order.
func (r *Registry) All() []Chunker {
	return r.chunkers
}

// ForDataset returns the chunkers reading the given dataset. Usually one,
// but the contract allows several (e.g. multiple scopes over one
// dataset). Empty when no chunker covers the dataset — datasets excluded
// from indexing (agent_debug_log, program, miniapp) return nothing.
func (r *Registry) ForDataset(dataset string) []Chunker {
	var out []Chunker
	for _, c := range r.chunkers {
		if c.Dataset() == dataset {
			out = append(out, c)
		}
	}
	return out
}
