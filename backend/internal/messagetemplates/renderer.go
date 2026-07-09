package messagetemplates

import "fmt"

// Renderer resolves the effective template for a Name (operator override else
// built-in default) and renders it. A non-empty override that fails to parse or
// execute (a bad operator edit) never drops a nudge: Render falls back to the
// built-in default and returns the override error for the caller to log.
type Renderer struct {
	overrides func() map[string]string
}

// NewRenderer builds a Renderer over an overrides source. A nil source (or a
// source returning nil) means "always use defaults".
func NewRenderer(overrides func() map[string]string) *Renderer {
	if overrides == nil {
		overrides = func() map[string]string { return nil }
	}
	return &Renderer{overrides: overrides}
}

// Render returns the rendered message for name. The returned string is always
// usable (default applied on override failure); the error is non-nil only when
// a non-empty override failed to render.
func (r *Renderer) Render(name Name, data any) (string, error) {
	text := Default(name)
	usedOverride := false
	if ov := r.overrides(); ov != nil {
		if custom, ok := ov[string(name)]; ok && custom != "" {
			text = custom
			usedOverride = true
		}
	}
	out, err := Execute(text, data)
	if err == nil {
		return out, nil
	}
	if usedOverride {
		if def, derr := Execute(Default(name), data); derr == nil {
			return def, fmt.Errorf("messagetemplates: override for %q failed, used default: %w", name, err)
		}
	}
	return "", err
}
