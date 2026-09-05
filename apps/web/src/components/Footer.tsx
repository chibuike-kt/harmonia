import {
  DiscordIcon,
  GitHubIcon,
  GlobeIcon,
  LinkedInIcon,
  XIcon,
} from "./icons";

interface FooterColumn {
  label: string;
  links: string[];
  /** A second, visually separated group under the same column (mockup's "Coming soon" break in Product). */
  secondary?: { label: string; links: string[] };
}

// Link labels only — same "real destinations don't exist yet" situation
// as Nav's mega menus; see docs/design/login-mockup-reference.html.
const FOOTER_COLUMNS: FooterColumn[] = [
  {
    label: "Product",
    links: [
      "Rooms",
      "Agent registry",
      "Handoffs",
      "Live activity",
      "Task orchestration",
    ],
    secondary: {
      label: "Coming soon",
      links: ["Tool execution", "Agent marketplace"],
    },
  },
  {
    label: "Developers",
    links: [
      "Documentation",
      "API reference",
      "Protocol (AACP)",
      "SDKs",
      "Developer forum",
      "Status",
    ],
  },
  {
    label: "Solutions",
    links: [
      "Software development",
      "Research workflows",
      "Customer operations",
    ],
  },
  {
    label: "Company",
    links: ["About us", "Careers", "Blog", "News", "Contact sales"],
  },
  {
    label: "Resources",
    links: ["Changelog", "Community", "Support", "Help center"],
  },
  {
    label: "Legal",
    links: ["Terms of use", "Privacy policy", "Security", "Other policies"],
  },
];

function FooterColumnView({ column }: { column: FooterColumn }) {
  return (
    <div className="flex min-w-[150px] flex-col gap-3">
      <span className="font-[family-name:var(--login-font-mono)] text-[13px] text-[var(--login-text-muted)]">
        {column.label}
      </span>
      {column.links.map((link) => (
        <a
          key={link}
          href="#"
          className="text-[15px] text-[var(--login-text)] hover:text-[var(--login-accent)] hover:underline"
        >
          {link}
        </a>
      ))}
      {column.secondary && (
        <>
          <span className="mt-1.5 font-[family-name:var(--login-font-mono)] text-[13px] text-[var(--login-text-muted)]">
            {column.secondary.label}
          </span>
          {column.secondary.links.map((link) => (
            <a
              key={link}
              href="#"
              className="text-[15px] text-[var(--login-text)] hover:text-[var(--login-accent)] hover:underline"
            >
              {link}
            </a>
          ))}
        </>
      )}
    </div>
  );
}

// Site footer from docs/design/login-mockup-reference.html. Reusable
// across pages (per the login-page-build-brief's scope note) — like Nav,
// takes no props.
export function Footer() {
  return (
    <footer className="border-t border-[var(--login-border)] px-5 pb-8 pt-14 sm:px-8">
      <div className="mx-auto grid max-w-[1100px] grid-cols-2 gap-x-8 gap-y-10 sm:grid-cols-3 md:flex md:flex-wrap md:gap-14">
        {FOOTER_COLUMNS.map((column) => (
          <FooterColumnView key={column.label} column={column} />
        ))}
      </div>

      <div className="mx-auto mt-12 flex max-w-[1100px] flex-col items-start gap-5 border-t border-[var(--login-border)] pt-5 sm:flex-row sm:items-center sm:justify-between sm:gap-0">
        <div className="flex items-center gap-4 text-[var(--login-text-secondary)]">
          <a href="#" aria-label="X" className="hover:text-[var(--login-text)]">
            <XIcon />
          </a>
          <a
            href="#"
            aria-label="GitHub"
            className="hover:text-[var(--login-text)]"
          >
            <GitHubIcon size={17} />
          </a>
          <a
            href="#"
            aria-label="LinkedIn"
            className="hover:text-[var(--login-text)]"
          >
            <LinkedInIcon />
          </a>
          <a
            href="#"
            aria-label="Discord"
            className="hover:text-[var(--login-text)]"
          >
            <DiscordIcon />
          </a>
        </div>

        <div className="flex items-center gap-2 font-[family-name:var(--login-font-mono)] text-xs text-[var(--login-text-muted)]">
          <p className="m-0">© 2026 harmonia</p>
          <a href="#" className="text-[var(--login-text-muted)] underline">
            Manage cookies
          </a>
        </div>

        <div className="flex items-center gap-2 rounded-lg border border-[var(--login-border-strong)] bg-[var(--login-surface-2)] px-3 py-1.5 text-xs text-[var(--login-text-secondary)]">
          <GlobeIcon />
          English
        </div>
      </div>
    </footer>
  );
}
