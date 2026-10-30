# show_widget rules

The chat draws `widget_code` inside a sandboxed frame below your message. The frame has no access to the app, its API, cookies or local files.

## Code
- Start with `<svg` for a diagram or illustration; anything else is an HTML fragment. Leave out doctype, `<html>`, `<head>` and `<body>`.
- Keep the background transparent and add no outer padding. The chat provides the card around the frame.
- The frame grows to fit your content. Do not use `position: fixed`, nested scroll areas or `requestFullscreen()`. The chat has its own expand button.
- Scripts run after the code is complete. Load a library with `<script src>` before the inline script that uses it.
- External files load only from `cdnjs.cloudflare.com`, `cdn.jsdelivr.net`, `unpkg.com` and `esm.sh` (scripts, styles, `fetch`), and fonts from `fonts.googleapis.com` and `fonts.gstatic.com`. Everything else is blocked.
- No comments in the code. They cost tokens and show nothing.
- Keep it under 128 KB.

## Colour and type
Use these CSS variables. They follow the app's light and dark theme, so every colour works in both:
- Text: `--text-primary`, `--text-secondary`, `--text-muted`
- Surfaces: `--surface-0` (page), `--surface-1` (card), `--surface-2` (raised)
- Lines: `--border`, `--border-strong`
- Roles: `--accent`, `--success` (gain), `--danger` (loss), `--warning`
- Fonts: `--font-sans`, `--font-mono`; radius `--radius`

In SVG, set `fill` and `stroke` with `var(--...)` too. Never hard-code a text colour. Use sentence case, two font weights (400 and 500) and no text smaller than 11px. No gradients, shadows or glow.

## Talking back to the chat
`sendPrompt(text)` puts `text` in the user's message box. The user reviews it and sends it. Use it for follow-ups that need you to think, such as "Explain why the curve bends here". Do sliders, filters and sums in the widget's own script. Mark a button that calls `sendPrompt` with a trailing "↗".

## Content
Do not put a title or the explanation inside the widget. Write those in your message. Do not repeat what the widget shows.
