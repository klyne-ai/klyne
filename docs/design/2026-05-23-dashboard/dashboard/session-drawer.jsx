// Session detail drawer. Opens over any page when a session is clicked.
// Merges /sessions/[id] and /insights/sessions/[id] — same view either entry.

function SessionDrawer({ id, onClose }) {
  // Mock detail derived from id
  const meta = {
    short: id ? id.slice(0, 8) : "bd3e7f78",
    full: id || "bd3e7f78-95a4-4b95-9a17-92858076d3d1",
    cli: "claude",
    state: "idle",
    started: "17h ago",
    lastMsg: "16h ago",
    project: "klyne",
    model: "claude-opus-4-7",
  };
  const stats = [
    { k: "Messages", v: "298", s: "166 user · 132 assistant" },
    { k: "↑ Tokens in", v: "18.0M", s: "17.3M cached · 630K fresh" },
    { k: "↓ Tokens out", v: "101K", s: "—" },
    { k: "Cost", v: "—", s: "subscription" },
  ];
  const ctxPct = 15;
  const tokenSeries = [31, 35, 40, 48, 60, 75, 84, 96, 110, 118, 128, 132, 141, 148, 153];

  return (
    <>
      <div className="drawer-scrim" onClick={onClose} />
      <aside className="drawer">
        <header className="drawer-head">
          <span className="dot" />
          <div>
            <div className="row" style={{ gap: 10 }}>
              <span className="kicker">Session</span>
              <span className="mono soft" style={{ fontSize: 12 }}>{meta.full}</span>
            </div>
            <div className="mono dim" style={{ fontSize: 11, marginTop: 2 }}>
              <span className="pill pill-claude" style={{ marginRight: 6 }}>{meta.cli}</span>
              {meta.project} · started {meta.started} · last msg {meta.lastMsg} · {meta.model}
            </div>
          </div>
          <button className="x" onClick={onClose}><window.Icons.x /></button>
        </header>

        <div className="drawer-body">
          <div className="col" style={{ gap: 16 }}>
            <div style={{ display: "grid", gridTemplateColumns: "repeat(4, 1fr)", gap: 10 }}>
              {stats.map((s) => (
                <div key={s.k} className="stat"><span className="k">{s.k}</span><span className="v">{s.v}</span><span className="s">{s.s}</span></div>
              ))}
            </div>

            <div className="card" style={{ padding: "14px 16px" }}>
              <div className="row" style={{ justifyContent: "space-between", marginBottom: 6 }}>
                <span className="mono soft" style={{ fontSize: 12 }}>Session {ctxPct}% full</span>
                <span className="mono dim" style={{ fontSize: 11 }}>153K of 1.0M · {meta.model}</span>
              </div>
              <div className="hbar"><div className="fill" style={{ transform: `scaleX(${ctxPct / 100})`, background: ctxPct > 70 ? "var(--alert)" : ctxPct > 50 ? "var(--warn)" : "var(--ok)" }} /></div>
            </div>

            <div className="card">
              <div className="card-head">
                <div className="title">Token usage over time <span className="sub">{tokenSeries.length} turns · 16:41 → 10:15</span></div>
              </div>
              <div style={{ padding: 18 }}>
                <TokenSeriesChart data={tokenSeries} />
              </div>
            </div>

            <div className="card">
              <div className="card-head">
                <div className="title">KLYNE_SUMMARY <span className="sub">importance 9 · auto-extracted</span></div>
              </div>
              <div style={{ padding: "14px 18px", fontSize: 12.5, color: "var(--fg-soft)", lineHeight: 1.6 }}>
                Closed the MCP-registration gap that kept <span className="mono">/klyne:reflect</span> from resolving outside the klyne project — promoted the klyne stdio server to user-scope <span className="mono">mcpServers</span> in <span className="mono">~/.claude.json</span>, removing duplicate per-project entries and surfacing the underlying bug that <span className="mono">scripts/install.sh</span> never invokes <span className="mono">klyne mcp install</span>.
              </div>
            </div>

            <div className="card">
              <div className="card-head">
                <div className="title">Advisors fired in this session <span className="sub">4 events</span></div>
              </div>
              <div className="col" style={{ padding: "8px 12px", gap: 6 }}>
                {window.FX.advisors.filter(a => a.session === "191eef49").slice(0, 4).map((a, i) => (
                  <div key={i} style={{ padding: "8px 10px", borderRadius: 6, background: "var(--bg-card-2)", borderLeft: `2px solid ${a.level === "alert" ? "var(--alert)" : "var(--warn)"}` }}>
                    <div className="row" style={{ justifyContent: "space-between", marginBottom: 4 }}>
                      <span className="kicker" style={{ color: a.level === "alert" ? "var(--alert)" : "var(--warn)" }}>{a.kind.replace(/_/g, " ")}</span>
                      <span className="mono dim" style={{ fontSize: 10.5 }}>{a.ago}</span>
                    </div>
                    <p style={{ margin: 0, fontSize: 11.5, color: "var(--fg-soft)", lineHeight: 1.5, display: "-webkit-box", WebkitLineClamp: 2, WebkitBoxOrient: "vertical", overflow: "hidden" }}>{a.body}</p>
                  </div>
                ))}
              </div>
            </div>

            <div className="card">
              <div className="card-head">
                <div className="title">Resume command</div>
                <button className="k-btn"><window.Icons.copy /> copy</button>
              </div>
              <div style={{ padding: "12px 16px", fontFamily: "var(--font-mono)", fontSize: 11.5, color: "var(--fg-soft)" }}>
                claude --resume {meta.full}
              </div>
            </div>
          </div>
        </div>
      </aside>
    </>
  );
}

function TokenSeriesChart({ data }) {
  const max = Math.max(...data) * 1.1;
  const w = 720, h = 200, p = 30;
  const stepX = (w - p * 2) / (data.length - 1);
  const points = data.map((v, i) => `${p + i * stepX},${h - p - (v / max) * (h - p * 2)}`).join(" ");
  return (
    <svg viewBox={`0 0 ${w} ${h}`} width="100%" height={h} preserveAspectRatio="none">
      {[0.25, 0.5, 0.75, 1].map(g => (
        <line key={g} x1={p} x2={w - p} y1={h - p - (h - p * 2) * g} y2={h - p - (h - p * 2) * g} stroke="var(--border-hair)" strokeDasharray="2 4" />
      ))}
      <polyline points={points} fill="none" stroke="var(--accent)" strokeWidth="1.5" />
      <polyline points={`${p},${h - p} ${points} ${w - p},${h - p}`} fill="color-mix(in oklch, var(--accent) 12%, transparent)" stroke="none" />
      {data.map((v, i) => (
        <circle key={i} cx={p + i * stepX} cy={h - p - (v / max) * (h - p * 2)} r={i === data.length - 1 ? 3 : 1.5} fill="var(--accent)" />
      ))}
    </svg>
  );
}

window.SessionDrawer = SessionDrawer;
