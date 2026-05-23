// Minimal stroke-icon set — 16x16, single-color, currentColor.
// Used for nav and inline affordances. Kept ultra-simple, no fills.

const Icon = ({ children, size = 16 }) => (
  <svg width={size} height={size} viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" strokeLinejoin="round">
    {children}
  </svg>
);

const Icons = {
  live: () => (
    <Icon>
      <circle cx="8" cy="8" r="2.2" />
      <path d="M4 4.5a5 5 0 0 0 0 7M12 4.5a5 5 0 0 1 0 7" />
    </Icon>
  ),
  productivity: () => (
    <Icon>
      <path d="M2.5 12.5V9M6 12.5V5M9.5 12.5V7.5M13 12.5V3.5" />
    </Icon>
  ),
  projects: () => (
    <Icon>
      <path d="M2.5 4.5a1 1 0 0 1 1-1h3l1.2 1.4h4.8a1 1 0 0 1 1 1V12a1 1 0 0 1-1 1h-9a1 1 0 0 1-1-1Z" />
    </Icon>
  ),
  insights: () => (
    <Icon>
      <path d="M2.5 13h11" />
      <path d="M3.5 10.5l3-3 2.5 2 4-4.5" />
      <circle cx="13" cy="5" r="0.8" />
    </Icon>
  ),
  runbooks: () => (
    <Icon>
      <path d="M3.5 2.8h7a1 1 0 0 1 1 1v9.4a1 1 0 0 1-1 1h-7Z" />
      <path d="M3.5 2.8v11.4" />
      <path d="M5.5 5.5h4M5.5 7.5h4M5.5 9.5h3" />
    </Icon>
  ),
  search: () => (
    <Icon>
      <circle cx="7" cy="7" r="3.6" />
      <path d="M10 10l3 3" />
    </Icon>
  ),
  refresh: () => (
    <Icon>
      <path d="M3 8a5 5 0 0 1 8.5-3.5L13 6M13 3v3h-3" />
      <path d="M13 8a5 5 0 0 1-8.5 3.5L3 10M3 13v-3h3" />
    </Icon>
  ),
  x: () => (<Icon><path d="M4 4l8 8M12 4l-8 8" /></Icon>),
  chev: () => (<Icon><path d="M6 4l4 4-4 4" /></Icon>),
  open: () => (<Icon><path d="M6 3h7v7" /><path d="M13 3l-7 7" /><path d="M11 9v4H3V5h4" /></Icon>),
  filter: () => (<Icon><path d="M2.5 4h11l-4 5v4l-3-1V9Z" /></Icon>),
  copy: () => (<Icon><rect x="5" y="5" width="8" height="8" rx="1.2" /><path d="M5 9V3h6" /></Icon>),
  sparkles: () => (<Icon><path d="M8 2.5v11M2.5 8h11M5 5l6 6M11 5l-6 6" /></Icon>),
  inbox: () => (<Icon><path d="M2.5 9.5l1.5-6h8l1.5 6v3a1 1 0 0 1-1 1h-9a1 1 0 0 1-1-1Z" /><path d="M2.5 9.5h3l1 2h3l1-2h3" /></Icon>),
  flame: () => (<Icon><path d="M8 13.5c2 0 3.5-1.5 3.5-3.5 0-2-2-2.5-2-5.5 0 1.5-2 2-2 4 0-1.5-1.5-2-1.5-1 0 1-1.5 1.5-1.5 3 0 2 1.5 3 3.5 3Z" /></Icon>),
  branch: () => (<Icon><circle cx="4" cy="4" r="1.4" /><circle cx="4" cy="12" r="1.4" /><circle cx="12" cy="6" r="1.4" /><path d="M4 5.5v5M4.7 11.6c4-1 7.3-2.4 7.3-5" /></Icon>),
};

window.Icon = Icon;
window.Icons = Icons;
