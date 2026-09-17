# Monochrome design system

**Status:** Accepted
**Date:** 2026-09-16
**Scope:** The entire product — main app (login, dashboard, rooms,
settings) and the IDE, both.

## The decision

True monochrome. Zero hue anywhere in Harmonia's own design — no teal,
no purple, no blue, no amber. Every previous meaning color used to
carry (liveness, provider identity, file type, card category, urgency)
gets carried by motion, weight, icon, or border style instead.

## Two explicit, deliberate exceptions — not gaps in the rule

**Third-party brand marks stay in their real, correct color** —
Google, GitHub, Anthropic, and OpenAI's logos are other companies'
trademarks, not Harmonia's design choice. Google's own guidelines
effectively require the color mark for sign-in buttons specifically.
Recoloring someone else's trademark to fit a house style isn't
restraint, it's just wrong.

**Diff views (removed/added lines) keep red/green.** This is the
highest-stakes surface in the product — misreading a diff has real
consequences, and red/green for removed/added is one of the most
universal, safety-load-bearing conventions in all of software. Every
other color-coded meaning in this system was decorative-adjacent by
comparison; this one genuinely protects against misreading a real
change to real code.

## Token palette — same names, new values

Every existing surface already references these custom properties, so
redefining them at the root is the correct, surgical way to apply this
system everywhere at once:

```css
:root {
  --bg: #050505;
  --surface: #101010;
  --surface-2: #191919;
  --border: #262626;
  --border-strong: #363636;
  --text: #F2F2F2;
  --text-secondary: #A3A3A3;
  --text-muted: #6B6B6B;

  /* "Accent" is no longer a hue — it's the brightest point on the
     grayscale, reserved for hover/interactive/emphasis states. Never
     used for anything decorative; if it's white, it means "this is
     interactive or currently focused." */
  --accent: #FFFFFF;
  --accent-glow: rgba(255, 255, 255, 0.22);
  --accent-dim: rgba(255, 255, 255, 0.08);
}
```

Dropping this into `globals.css` (and each mockup's equivalent root
block) correctly repaints everything that already used
`var(--bg)`/`var(--text)`/`var(--accent)` etc. — no per-component
rewrite needed for the base palette. What DOES need explicit rework is
everything that used a *second* semantic hue beyond this core set (see
below).

## Semantic replacements — what carries meaning now that color can't

**Liveness / active / presence** (room live status, agent running
state, presence rings, live cursors) → a gentle, real breathing/pulse
animation on a white ring or dot. Motion signals "happening right now"
the same way color used to — this is already a well-understood,
accessible convention (recording indicators, typing indicators), not a
downgrade.

**Provider identity** (agent tags) → the real logo plus the agent's
name already fully identifies it; color was redundant here. Tags go
neutral (`--surface-2` background, `--text-secondary` text) — no
identity is actually lost.

**File-type icons** → real icon-theme shapes (Material Icon Theme /
vscode-icons), rendered in a single neutral tone (`--text-secondary`),
relying on silhouette recognition instead of color. Worth stating
honestly: this trades some scan speed for the constraint — shape
recognition works, but it's genuinely a little slower than color at a
glance. Accepted tradeoff, not an oversight.

**Card categories** (task / handoff / approval) → a distinct icon per
type, the text label, and a distinct **border style** — solid for
tasks, dashed for handoffs — instead of a distinct border hue.

**Warning / urgency** (approval-needed state) → a bold border weight, a
real warning icon, and the same breathing-pulse treatment liveness
uses — motion and weight carry urgency instead of amber.

**The orb** (brand mark, thinking/typing indicator) → redrawn in white
with varying opacity per dot for depth, same technique as before, same
shape and motion identity, just monochrome.

## What this does NOT change

Layout, spacing, typography, structure — everything decided in every
prior design pass (the room mockup's card grammar, the IDE's VS Code-
style chrome, the settings modal's categories) stays exactly as
designed. This is a palette and semantic-signaling system change, not a
redesign of anything structural.
