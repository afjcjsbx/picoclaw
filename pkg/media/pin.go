package media

import "sync"

// PinScope keeps an inbound message's attachments alive throughout its turn.
// Stores without lifecycle pinning retain their existing behavior.
func PinScope(store MediaStore, scope string) func() {
	if s, ok := store.(*FileMediaStore); ok {
		s.mu.Lock()
		defer s.mu.Unlock()
		refs := make([]string, 0, len(s.scopeToRefs[scope]))
		for ref := range s.scopeToRefs[scope] {
			refs = append(refs, ref)
		}
		return s.pinLocked(refs)
	}
	return func() {}
}

// PinRefs protects files while a channel is delivering them, including retries.
func PinRefs(store MediaStore, refs ...string) func() {
	if s, ok := store.(*FileMediaStore); ok {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.pinLocked(refs)
	}
	return func() {}
}

func (s *FileMediaStore) pinLocked(refs []string) func() {
	paths := make(map[string]struct{})
	for _, ref := range refs {
		if entry, ok := s.refs[ref]; ok {
			paths[entry.path] = struct{}{}
		}
	}
	for path := range paths {
		s.pathPins[path]++
	}
	return sync.OnceFunc(func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		for path := range paths {
			s.pathPins[path]--
			if s.pathPins[path] == 0 {
				delete(s.pathPins, path)
			}
		}
		for ref, entry := range s.refs {
			if _, ok := paths[entry.path]; ok {
				entry.lastAccess = s.nowFunc()
				s.refs[ref] = entry
			}
		}
		_ = s.retryDeletesLocked()
	})
}
