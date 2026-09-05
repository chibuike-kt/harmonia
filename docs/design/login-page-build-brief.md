# Login page design implementation — for Claude Code

**Reference file:** `docs/design/login-mockup.html`, committed alongside this
brief. That file is the exact visual target — colors, type, spacing, layout,
hover behavior. Port it faithfully; don't reinterpret it. If something in it
is ambiguous or can't translate directly to React/Next.js, stop and ask
rather than improvising a different visual outcome.

## Objective

Replace the current unstyled login page (`apps/web/src/app/login/page.tsx`,
built in phase 3 step 6) with the designed version, while keeping every
piece of real functionality that page already has. This is a visual/
structural redesign, not a rebuild of the auth flow.

## What already works — do not touch the underlying behavior

- The GitHub and Google buttons are plain `<a>` tags pointing at
  `apiUrl("/v1/auth/github/login")` / `.../google/login"` — full-page
  navigations to the Go backend, not client-side routes. Keep them as `<a>`,
  not `next/link`, for the same reason noted in that page's original build:
  these leave the Next app entirely.
- `apiUrl`/`apiFetch` in `src/lib/api.ts` are the only sanctioned way to
  reference the backend's origin. Don't hardcode a URL anywhere in the new
  components.

## What to build

1. **`components/Nav.tsx`** — the top nav from the mockup: brand mark, four
   hover-triggered dropdown groups (Meet Harmonia, Platform, Solutions,
   Resources — each anchored to its own trigger, sized to its own content,
   not full-width), a plain Pricing link, and the solid "Try Harmonia"
   button. Hover behavior in the mockup uses CSS `:hover`/`:focus-within` —
   carry that over rather than reintroducing JS click-toggle state; it's
   simpler and gets keyboard accessibility for free.

2. **`components/Footer.tsx`** — the six-column footer plus the bottom row
   (social icons, copyright/cookie line, region pill), exactly as laid out
   in the mockup.

3. **Design tokens** — pull the mockup's `:root` custom properties
   (`--bg`, `--surface`, `--accent`, etc.) into wherever this project's
   global styles live (`globals.css` or the Tailwind config, whichever this
   codebase already uses — check before adding a second system). Load
   Space Grotesk and JetBrains Mono via `next/font/google`, not the raw
   `<link>` tags the mockup uses — that's a mockup-only shortcut; Next's
   font loading avoids the layout shift and gives proper font-display
   behavior.

4. **Update `login/page.tsx`** to use `Nav` and `Footer`, with the hero
   headline/subhead and the card (GitHub button, divider, Google button,
   privacy footnote) matching the mockup. No Apple button, no email/
   password fields — those were explicitly removed from the final mockup
   and stay out, per ADR-002.

## Open decision: the orb's color

The mockup's orb is my own hand-drawn canvas approximation, colored to
match the accent teal. The real component — `npm install thinking-orbs` —
is **strictly monochrome by design**: light dots on dark backgrounds or
dark dots on light, no color prop, no way to theme it teal without forking
the library or writing a custom renderer instead of using the package.

**Default to shipping it as the library provides it** — `<ThinkingOrb
state="working" size={20} theme="dark" />` in the nav, monochrome. That's
the honest, low-risk path and matches every other place in this build
where "use the real thing as it actually behaves" won over "make the mockup
literally true." If the teal orb is a hard requirement, that's a separate,
deliberate decision to fork or replace the library — flag it back to me
rather than quietly forking it as part of this task.

## Things the mockup doesn't cover — use judgment, then report what you chose

- **Mobile/responsive behavior.** The mockup was never tested below
  desktop width — the mega-menus, six-column footer, and hero type scale
  will all break on a phone. This needs a real responsive treatment
  (stacked footer columns, a collapsed/hamburger nav below some breakpoint,
  scaled-down hero type), not a direct port that happens to overflow on
  mobile. Match the mockup's *language* (same colors, same restraint) at
  small sizes, not its exact desktop layout.
- **`prefers-reduced-motion`.** Check whether `thinking-orbs` already
  respects it. If it doesn't, that's worth noting, not silently patching
  around by forking the library for this alone.
- **Nav/footer links.** Almost everything points at `#` in the mockup —
  Docs, Pricing, Blog, and most of the footer columns don't have real
  destinations yet. Keep them as inert links for now rather than inventing
  routes or fake pages to make them "work." This is real content debt,
  already flagged repeatedly during the design pass — don't quietly resolve
  it by making something up.

## Scope boundary

This brief covers the login page only. `Nav` and `Footer` should be built
as genuinely reusable components so `connect-agents` and the room screens
can adopt them later — but don't retrofit those pages as part of this task.
That's a separate, deliberate follow-up once the login page's version is
reviewed and settled.

## Definition of done

The login page visually matches `docs/design/login-mockup.html` at desktop
width, degrades sensibly on mobile, GitHub/Google buttons still hit the
real backend routes exactly as before, `next/font` loads the real
typefaces, lint/typecheck/build all pass. State plainly in your report
which parts are a direct, confident port versus a judgment call you made
where the mockup didn't specify — same reporting discipline as every
backend phase so far.
