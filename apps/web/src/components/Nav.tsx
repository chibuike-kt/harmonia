"use client";

import { ThinkingOrb } from "thinking-orbs";
import { ChevronDownIcon, MenuIcon } from "./icons";

interface MegaMenuColumn {
  label: string;
  links: string[];
}

interface MegaMenuGroup {
  label: string;
  columns: MegaMenuColumn[];
}

// Link labels only — almost everything here points at "#" in the design
// (docs/design/login-mockup-reference.html): real destinations don't
// exist yet, and inventing routes to make these "work" is out of scope
// per the build brief. Kept as inert links until that content exists.
const NAV_GROUPS: MegaMenuGroup[] = [
  {
    label: "Meet Harmonia",
    columns: [
      {
        label: "Product",
        links: ["Rooms", "Agent registry", "Handoffs", "Live activity"],
      },
      {
        label: "Developers",
        links: ["Documentation", "API reference", "Protocol (AACP)"],
      },
      { label: "Company", links: ["Pricing", "Blog", "Changelog"] },
    ],
  },
  {
    label: "Platform",
    columns: [
      {
        label: "Core",
        links: ["Task orchestration", "Event log", "Realtime presence"],
      },
      {
        label: "Security",
        links: ["Encryption", "Audit trail", "Permissions"],
      },
    ],
  },
  {
    label: "Solutions",
    columns: [
      {
        label: "By use case",
        links: [
          "Software development",
          "Research workflows",
          "Customer operations",
        ],
      },
    ],
  },
  {
    label: "Resources",
    columns: [
      {
        label: "Resources",
        links: ["Documentation", "Blog", "Changelog", "Community"],
      },
    ],
  },
];

// Hover treatment for mega-menu links only: an accent underline that
// grows in from the left, plus a rounded background highlight — the
// radius matches the buttons elsewhere in this design (rounded-lg). The
// top-level row (triggers + Pricing) deliberately does not use this —
// on request, hovering those only dims their siblings, nothing else.
const LINK_HOVER = [
  "relative rounded-lg transition-colors",
  "after:absolute after:inset-x-2 after:-bottom-0.5 after:h-px after:origin-left",
  "after:scale-x-0 after:bg-[var(--login-accent)] after:transition-transform after:duration-200",
  "hover:after:scale-x-100",
].join(" ");

function MegaMenuColumnView({ column }: { column: MegaMenuColumn }) {
  return (
    <div className="flex min-w-[160px] flex-col gap-3.5 px-8 first:pl-0 last:pr-0">
      <span className="font-[family-name:var(--login-font-mono)] text-xs text-[var(--login-text-muted)]">
        {column.label}
      </span>
      {column.links.map((link) => (
        <a
          key={link}
          href="#"
          className={`${LINK_HOVER} -mx-2 -my-1 px-2 py-1 text-sm text-[var(--login-text)] hover:bg-[var(--login-surface-2)] hover:text-[var(--login-accent)] group-has-[a:hover]/menu:not-hover:text-[var(--login-text-muted)]`}
        >
          {link}
        </a>
      ))}
    </div>
  );
}

// The wrapper carries the color state for the whole trigger (button +
// chevron inherit via plain CSS inheritance, so this is the only element
// that needs a text-color class) — see the group-has-hover/not-hover
// pairing below, which is why hover doesn't fight the row's sibling-dim
// rule: the two conditions can't both be true on the same element.
function NavGroup({ group }: { group: MegaMenuGroup }) {
  return (
    <div
      className={[
        "group group/item relative",
        "text-[var(--login-text-secondary)]",
        "focus-within:text-[var(--login-text)]",
        "group-has-[:hover]/navrow:not-hover:text-[var(--login-text-muted)]",
        "transition-colors duration-150",
      ].join(" ")}
    >
      <button
        type="button"
        aria-haspopup="true"
        className="-mx-2 -my-1.5 flex items-center gap-1.5 px-2 py-1.5 text-base"
      >
        {group.label}
        <ChevronDownIcon className="transition-transform duration-150 ease-out group-hover:rotate-180 group-focus-within:rotate-180" />
      </button>
      <div className="absolute left-0 top-full z-10 mt-3.5 hidden whitespace-nowrap rounded-xl border border-[var(--login-border)] bg-[var(--login-surface)] p-9 group-hover:flex group-focus-within:flex">
        <div className="group/menu flex divide-x divide-[var(--login-border)]">
          {group.columns.map((column) => (
            <MegaMenuColumnView key={column.label} column={column} />
          ))}
        </div>
      </div>
    </div>
  );
}

// Plus/minus toggle for the mobile accordion, built from two bars rather
// than a separate icon asset — the vertical bar collapses on
// details[open] (Tailwind's group-open:), leaving the horizontal one as
// the "−".
function AccordionToggle() {
  return (
    <span className="relative flex h-4 w-4 shrink-0 items-center justify-center text-[var(--login-text-secondary)]">
      <span className="absolute h-px w-3 bg-current" />
      <span className="absolute h-3 w-px bg-current transition-transform duration-150 group-open:scale-y-0" />
    </span>
  );
}

function MobileAccordionRow({ group }: { group: MegaMenuGroup }) {
  return (
    <details className="group">
      <summary className="flex cursor-pointer list-none items-center justify-between px-5 py-4 text-[15px] text-[var(--login-text)] [&::-webkit-details-marker]:hidden">
        {group.label}
        <AccordionToggle />
      </summary>
      <div className="flex flex-col gap-1 px-5 pb-4">
        {group.columns
          .flatMap((column) => column.links)
          .map((link) => (
            <a
              key={link}
              href="#"
              className="rounded-lg px-3 py-2 text-sm text-[var(--login-text-secondary)] hover:bg-[var(--login-surface-2)] hover:text-[var(--login-text)]"
            >
              {link}
            </a>
          ))}
      </div>
    </details>
  );
}

// Top marketing nav from docs/design/login-mockup-reference.html. Reusable
// across pages (per the login-page-build-brief's scope note) — takes no
// props, since every link and label here is fixed, shared chrome.
//
// Mega menus open on :hover/:focus-within (Tailwind's group-hover /
// group-focus-within), not React state — same mechanism the mockup uses,
// which is also what gives keyboard users a working, focus-driven
// disclosure for free. The sibling-dimming effect (hovering one link
// mutes the others in its group) is CSS :has() as well — no JS state
// anywhere in this component.
export function Nav() {
  return (
    <nav className="relative flex items-center justify-between border-b border-[var(--login-border)] px-5 py-4 sm:px-8">
      <div className="flex items-center gap-2.5">
        {/* The library ships two tuned presets, 20 and 64, as separate
            designs rather than one scaled by the other (see its own
            docs). Compared both at nav scale: native 20 reads as a
            sparse handful of dots, while the 64 preset rendered small
            keeps its fuller dot density and reads more clearly as an
            orb, so it's used here constrained down via style rather
            than at its native size. */}
        <ThinkingOrb
          state="working"
          size={64}
          theme="dark"
          style={{ width: 45, height: 45 }}
        />
        {/* Pirata One is a blackletter face — legible at a glance needs
            noticeably more size than a sans wordmark would at the same
            spot, so this runs larger than the brand mark next to it
            would otherwise call for. No tracking/weight utility: the
            font ships exactly one weight and its letterforms are
            already tight, hand-tuned for this style. */}
        <span className="font-[family-name:var(--font-pirata-one)] text-3xl text-[var(--login-text)]">
          Harmonia
        </span>
      </div>

      {/* Desktop nav */}
      <div className="hidden items-center gap-11 md:flex">
        <div className="group/navrow flex items-center gap-[30px]">
          {NAV_GROUPS.map((group) => (
            <NavGroup key={group.label} group={group} />
          ))}
          <a
            href="#"
            className="-mx-2 -my-1 px-2 py-1 text-base text-[var(--login-text-secondary)] transition-colors duration-150 group-has-[:hover]/navrow:not-hover:text-[var(--login-text-muted)]"
          >
            Pricing
          </a>
        </div>
        <button
          type="button"
          className="flex h-10 items-center rounded-lg bg-[var(--login-text)] px-[18px] text-[15px] font-medium text-[var(--login-bg)] hover:bg-[#D5D8DC]"
        >
          Try Harmonia
        </button>
      </div>

      {/* Mobile nav: an accordion panel below the (still visible) nav
          bar. Each top-level item is its own <details> row — zero JS,
          same as the desktop hover menus — separated by hairlines, no
          outer border/box around the panel itself. */}
      <details className="md:hidden">
        <summary
          aria-label="Menu"
          className="flex list-none items-center justify-center rounded-lg p-2 text-[var(--login-text)] hover:bg-[var(--login-surface-2)] [&::-webkit-details-marker]:hidden"
        >
          <MenuIcon />
        </summary>
        <div className="absolute inset-x-0 top-full z-10 flex flex-col divide-y divide-[var(--login-border)] bg-[var(--login-surface)]">
          {NAV_GROUPS.map((group) => (
            <MobileAccordionRow key={group.label} group={group} />
          ))}
          <a
            href="#"
            className="px-5 py-4 text-[15px] text-[var(--login-text)] hover:bg-[var(--login-surface-2)]"
          >
            Pricing
          </a>
          <div className="p-5">
            <button
              type="button"
              className="flex h-11 w-full items-center justify-center rounded-lg bg-[var(--login-text)] text-[15px] font-medium text-[var(--login-bg)] hover:bg-[#D5D8DC]"
            >
              Try Harmonia
            </button>
          </div>
        </div>
      </details>
    </nav>
  );
}
