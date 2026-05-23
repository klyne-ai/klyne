// Productivity page wrapper.
// Reuses the existing v4-combined.jsx artboard verbatim, with the dashboard
// chrome around it. The artboard is the user-approved design; we just embed.

function PageProductivity() {
  return (
    <div style={{ margin: "-22px -26px -60px", display: "flex", flexDirection: "column", minHeight: "calc(100vh - 52px)" }}>
      <div style={{ padding: "22px 26px 14px", borderBottom: "1px solid var(--border-hair)", display: "flex", alignItems: "flex-end", justifyContent: "space-between", gap: 18 }}>
        <div>
          <h1 style={{ margin: "0 0 4px", fontSize: 22, fontWeight: 500, letterSpacing: "-0.02em", color: "var(--fg)" }}>Productivity</h1>
          <p style={{ margin: 0, fontSize: 12.5, color: "var(--fg-muted)", maxWidth: "60ch", lineHeight: 1.55 }}>
            The standup digest — a Friday-afternoon answer to "what did I ship this week, and at what cost?"
            Aggregated across every project + service from yesterday's worklog reflections, signals, and git state.
          </p>
        </div>
        <div className="row" style={{ gap: 8, flexShrink: 0 }}>
          <span className="mono dim" style={{ fontSize: 11 }}>loaded 17h ago</span>
          <button className="k-btn"><window.Icons.refresh /></button>
        </div>
      </div>
      <div style={{ overflow: "auto", flex: 1, background: "var(--bg)" }}>
        <div style={{ minWidth: 1180 }}>
          <window.V4 d={window.DAY} />
        </div>
      </div>
    </div>
  );
}

window.PageProductivity = PageProductivity;
