package router

import "context"

// ReportsLoadedWindow reports whether h states the request window a loaded
// model serves with (ROUTE-D8): Lemonade, Ollama and LM Studio.
func (h *Host) ReportsLoadedWindow() bool { return h != nil && (h.windowAtLoad || h.loadedWindow) }

// LoadedWindow reads the request window h serves name with now, in tokens,
// from the server itself (ROUTE-D8). On a server that can load a model
// without a request (Ollama), a model whose window is unknown is loaded
// first, as the request about to be sent would load it. It is 0 when the
// window stays unknown and for a server that reports no loaded window.
func LoadedWindow(ctx context.Context, h *Host, name string) int64 {
	if !h.ReportsLoadedWindow() || name == "" {
		return 0
	}
	read := func() int64 {
		var c modelContext
		switch {
		case h.modelMetadata != nil:
			c = h.modelMetadata(ctx, h, []string{name})[name].context
		case h.modelContexts != nil:
			c = h.modelContexts(ctx, h, []string{name})[name]
		}
		return validEffective(c.effective, c.maximum)
	}
	window := read()
	if window == 0 && h.load != nil && ctx.Err() == nil && h.load(ctx, h, name) == nil {
		window = read()
	}
	return window
}
