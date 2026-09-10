package merge

// Patch applies RFC 7386 JSON Merge Patch semantics to dst.
// Keys with nil value in patch delete the corresponding key in dst.
func Patch(dst, patch map[string]any) map[string]any {
	if dst == nil {
		dst = make(map[string]any)
	}
	for k, v := range patch {
		if v == nil {
			delete(dst, k)
			continue
		}
		if pm, ok := v.(map[string]any); ok {
			if dm, ok := dst[k].(map[string]any); ok {
				dst[k] = Patch(dm, pm)
				continue
			}
		}
		dst[k] = v
	}
	return dst
}
