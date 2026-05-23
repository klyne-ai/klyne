// Live: every coding session currently active or recently idle.
// One job: situational awareness across multiple AI terminals.
// Layout: 3-column grid (auto-adapts to 2 at narrow widths).
// Sidebar auto-collapses on this page so tiles get max screen real estate.

const { useState: useStateLive } = React;

function PageLive({ onOpenSession }) {
  const sessions = window.FX.liveSessions;
  const [filter, setFilter] = useStateLive("");
  const [cli, setCli] = useStateLive("all");
  const [showIdle, setShowIdle] = useStateLive(true);

  const filtered = sessions
    .filter(s => (cli === "all" ? true : s.cli === cli))
    .filter(s => (showIdle ? true : s.state === "live"))
    .filter(s => !filter || (s.project + s.id + s.branch).toLowerCase().includes(filter.toLowerCase()));

  const liveCount = sessions.filter(s => s.state === "live").length;
  const idleCount = sessions.filter(s => s.state !== "live").length;

  return (
    <div>
      <header className="page-head">
        <div>
          <h1>Live</h1>
          <p className="lede">
            Every AI terminal across every project, in one window. Multiple agents running in parallel
            stop falling off your radar — see which ones are awaiting your input, which are streaming,
            and what each one just said.
          </p>
        </div>
        <div className="actions">
          <span className="mono" style={{ color: "var(--ok)", fontSize: 11, whiteSpace: "nowrap" }}>
            <span className="dot ok" style={{ display: "inline-block", marginRight: 6 }} />
            {liveCount} live
          </span>
          <span className="mono dim" style={{ fontSize: 11, whiteSpace: "nowrap" }}>· {idleCount} idle</span>
        </div>
      </header>

      <div className="toolbar" style={{ marginTop: 0 }}>
        <div className="field" style={{ minWidth: 240 }}>
          <window.Icons.search />
          <input placeholder="Filter by project, branch, or session…" value={filter} onChange={(e) => setFilter(e.target.value)} />
        </div>
        <div className="field">
          <span className="lbl">cli</span>
          <select value={cli} onChange={(e) => setCli(e.target.value)}>
            <option value="all">all</option>
            <option value="claude">claude</option>
            <option value="codex">codex</option>
          </select>
        </div>
        <label className="row" style={{ gap: 8, cursor: "pointer", padding: "6px 10px", background: "var(--bg-card)", border: "1px solid var(--border-hair)", borderRadius: 8, fontSize: 12 }}>
          <input type="checkbox" checked={showIdle} onChange={(e) => setShowIdle(e.target.checked)} />
          <span>show idle</span>
        </label>
        <div className="grow" />
        <span className="mono dim" style={{ fontSize: 11, whiteSpace: "nowrap" }}>30m recency window · sorted by activity</span>
      </div>

      <div style={{
        display: "grid",
        gridTemplateColumns: "repeat(auto-fill, minmax(300px, 1fr))",
        gap: 14,
      }}>
        {filtered.map((s) => (
          <SessionTile key={s.id} s={s} onOpen={() => onOpenSession(s.id)} />
        ))}
      </div>

      {filtered.length === 0 && (
        <div className="card" style={{ padding: "40px 20px", textAlign: "center", color: "var(--fg-muted)" }}>
          No sessions match these filters.
        </div>
      )}
    </div>
  );
}

function SessionTile({ s, onOpen }) {
  const isLive = s.state === "live";
  const ctxColor = s.ctxPct > 70 ? "var(--alert)" : s.ctxPct > 50 ? "var(--warn)" : "var(--ok)";
  return (
    <div className="card" style={{
      overflow: "hidden",
      display: "flex", flexDirection: "column",
      borderColor: isLive ? "color-mix(in oklch, var(--ok) 30%, var(--border-hair))" : "var(--border-hair)",
      boxShadow: isLive ? "0 0 0 1px color-mix(in oklch, var(--ok) 18%, transparent), 0 10px 30px -16px color-mix(in oklch, var(--ok) 30%, transparent)" : "none",
    }}>
      {/* Header */}
      <div style={{ padding: "12px 14px", borderBottom: "1px solid var(--border-hair)" }}>
        <div className="row" style={{ justifyContent: "space-between", marginBottom: 6, gap: 8 }}>
          <div className="row" style={{ gap: 8, minWidth: 0, flex: 1, overflow: "hidden" }}>
            <span className={"dot " + (isLive ? "ok" : "")} style={{ flexShrink: 0 }} />
            <span style={{ fontSize: 13.5, color: "var(--fg)", fontWeight: 500, whiteSpace: "nowrap", flexShrink: 0 }}>{s.project}</span>
            <span className="mono dim" style={{ fontSize: 11, whiteSpace: "nowrap", overflow: "hidden", textOverflow: "ellipsis", minWidth: 0 }}>{s.id}</span>
          </div>
          <div className="row" style={{ gap: 6, flexShrink: 0 }}>
            <span className={"pill " + (s.cli === "claude" ? "pill-claude" : "pill-codex")}>{s.cli}</span>
            <span className="mono dim" style={{ fontSize: 11, whiteSpace: "nowrap" }}>{s.lastAgo} ago</span>
          </div>
        </div>
        <div className="row" style={{ gap: 12, fontSize: 11, minWidth: 0 }}>
          <span className="row mono muted" style={{ gap: 6, minWidth: 0, flex: 1 }}>
            <window.Icons.branch />
            <span style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{s.branch}</span>
          </span>
          <span className="mono muted" style={{ whiteSpace: "nowrap" }}>{s.msgs} · ↓ {s.tokens}</span>
          <span className="mono" style={{ color: ctxColor, whiteSpace: "nowrap" }}>ctx {s.ctxPct}%</span>
        </div>
      </div>

      {/* Tail messages */}
      <div style={{ padding: "10px 14px", display: "flex", flexDirection: "column", gap: 10, flex: 1, overflow: "hidden", maxHeight: 280 }}>
        {s.tail.slice(0, 3).map((m, i) => (
          <div key={i} style={{
            borderLeft: "2px solid color-mix(in oklch, var(--accent) 30%, transparent)",
            paddingLeft: 10,
          }}>
            <div className="row" style={{ gap: 8, marginBottom: 2 }}>
              <span className="kicker" style={{ color: "var(--accent)" }}>{m.role}</span>
              <span className="mono dim" style={{ fontSize: 10.5 }}>{m.t} ago</span>
            </div>
            <p style={{ margin: 0, fontSize: 12.5, color: "var(--fg-soft)", lineHeight: 1.5, display: "-webkit-box", WebkitLineClamp: 3, WebkitBoxOrient: "vertical", overflow: "hidden" }}>{m.text}</p>
          </div>
        ))}
      </div>

      {/* Foot */}
      <div style={{ padding: "8px 14px", borderTop: "1px solid var(--border-hair)", display: "flex", alignItems: "center", justifyContent: "space-between", background: "var(--bg-card-2)", gap: 8 }}>
        <span className="mono dim" style={{ fontSize: 10.5, whiteSpace: "nowrap", overflow: "hidden", textOverflow: "ellipsis" }}>{isLive ? "streaming" : "idle"} · last msg {s.lastAgo} ago</span>
        <button className="k-btn" onClick={onOpen} style={{ padding: "4px 10px", flexShrink: 0 }}>open <window.Icons.chev /></button>
      </div>
    </div>
  );
}

window.PageLive = PageLive;
