// Runbooks: cleaner card grid + a left sidebar of scopes and tags
// (filter-driven). Same data shape, more glanceable. Same content,
// less visual noise than the old chip-cluttered toolbar.

const { useState: useStateRb } = React;

function PageRunbooks() {
  const R = window.FX.runbooks;
  const [scope, setScope] = useStateRb("all");
  const [project, setProject] = useStateRb("all");
  const [tag, setTag] = useStateRb(null);
  const [q, setQ] = useStateRb("");

  const filtered = R.items.filter((r) => {
    if (scope === "global" && r.scope !== "global") return false;
    if (scope === "project" && r.scope !== "project") return false;
    if (project !== "all" && r.project !== project) return false;
    if (tag && !r.tags.includes(tag)) return false;
    if (q && !(r.title + " " + r.body).toLowerCase().includes(q.toLowerCase())) return false;
    return true;
  });

  const grouped = filtered.reduce((acc, r) => {
    const k = r.scope === "global" ? "Global" : `Project · ${r.project}`;
    (acc[k] = acc[k] || []).push(r);
    return acc;
  }, {});

  return (
    <div>
      <header className="page-head">
        <div>
          <h1>Runbooks</h1>
          <p className="lede">
            Pre-execution memory klyne checks before Claude runs operational shell commands.
            Add via <span className="mono">klyne remember this …</span> (project-scoped) or <span className="mono">klyne remember this globally …</span>.
            Used to recall a known-good runbook instead of relying on session memory.
          </p>
        </div>
        <div className="actions">
          <button className="k-btn">+ new runbook</button>
          <button className="k-btn"><window.Icons.copy /> Export</button>
        </div>
      </header>

      <div style={{ display: "grid", gridTemplateColumns: "220px 1fr", gap: 20, alignItems: "start" }}>
        {/* Sidebar filters */}
        <aside className="card" style={{ padding: 14, position: "sticky", top: 0 }}>
          <div className="col" style={{ gap: 16 }}>
            <div>
              <div className="kicker" style={{ marginBottom: 8 }}>Scope</div>
              <div className="col" style={{ gap: 2 }}>
                {[
                  ["all", `All (${R.items.length})`],
                  ["global", `Global (${R.items.filter(r => r.scope === "global").length})`],
                  ["project", `Project (${R.items.filter(r => r.scope === "project").length})`],
                ].map(([k, label]) => (
                  <button key={k}
                    className="k-btn k-btn--ghost"
                    onClick={() => setScope(k)}
                    style={{
                      justifyContent: "flex-start",
                      background: scope === k ? "var(--bg-card-2)" : "transparent",
                      color: scope === k ? "var(--fg)" : "var(--fg-soft)",
                      border: scope === k ? "1px solid var(--border-hair)" : "1px solid transparent",
                      padding: "6px 10px", fontSize: 12, fontFamily: "var(--font-sans)",
                    }}
                  >
                    {label}
                  </button>
                ))}
              </div>
            </div>

            <div>
              <div className="kicker" style={{ marginBottom: 8 }}>Project</div>
              <select
                value={project}
                onChange={(e) => setProject(e.target.value)}
                style={{
                  width: "100%",
                  background: "var(--bg-card-2)", color: "var(--fg)",
                  border: "1px solid var(--border-hair)", borderRadius: 6,
                  padding: "6px 8px", fontFamily: "var(--font-sans)", fontSize: 12,
                }}
              >
                <option value="all">All projects</option>
                {Array.from(new Set(R.items.map(r => r.project).filter(Boolean))).map(p => (
                  <option key={p} value={p}>{p}</option>
                ))}
              </select>
            </div>

            <div>
              <div className="kicker" style={{ marginBottom: 8 }}>Tags</div>
              <div className="row" style={{ flexWrap: "wrap", gap: 4 }}>
                {R.tags.map((t) => (
                  <button key={t} onClick={() => setTag(tag === t ? null : t)}
                    className="pill"
                    style={{
                      cursor: "pointer",
                      background: tag === t ? "color-mix(in oklch, var(--accent) 18%, var(--bg-card-2))" : undefined,
                      color: tag === t ? "var(--accent)" : undefined,
                      borderColor: tag === t ? "color-mix(in oklch, var(--accent) 40%, transparent)" : undefined,
                    }}
                  >
                    {t}
                  </button>
                ))}
              </div>
            </div>
          </div>
        </aside>

        {/* List */}
        <section>
          <div className="toolbar" style={{ margin: "0 0 16px" }}>
            <div className="field" style={{ flex: 1 }}>
              <window.Icons.search />
              <input placeholder="Search title, body, tag…" value={q} onChange={e => setQ(e.target.value)} style={{ width: "100%" }} />
            </div>
            <span className="mono dim" style={{ fontSize: 11 }}>{filtered.length} of {R.items.length}</span>
          </div>

          {Object.entries(grouped).map(([groupLabel, items]) => (
            <div key={groupLabel} style={{ marginBottom: 24 }}>
              <div className="row" style={{ justifyContent: "space-between", marginBottom: 10 }}>
                <h3 style={{ margin: 0, fontSize: 13, color: "var(--fg)", fontWeight: 500 }}>{groupLabel}</h3>
                <span className="mono dim" style={{ fontSize: 11 }}>{items.length} runbook{items.length === 1 ? "" : "s"}</span>
              </div>
              <div style={{ display: "grid", gridTemplateColumns: "repeat(auto-fill, minmax(360px, 1fr))", gap: 12 }}>
                {items.map((r) => <RunbookCard key={r.id} r={r} />)}
              </div>
            </div>
          ))}
        </section>
      </div>
    </div>
  );
}

function RunbookCard({ r }) {
  return (
    <div className="card" style={{ padding: 14, display: "flex", flexDirection: "column", gap: 10 }}>
      <div className="row" style={{ justifyContent: "space-between" }}>
        <span className={"pill " + (r.scope === "global" ? "pill-info" : "")} style={{ textTransform: "uppercase", fontSize: 10 }}>
          {r.scope === "global" ? "global" : r.project}
        </span>
        <span className="mono dim" style={{ fontSize: 10.5 }}>{r.id} · {r.ago}</span>
      </div>
      <h4 style={{ margin: 0, fontSize: 13.5, color: "var(--fg)", fontWeight: 500, lineHeight: 1.4 }}>{r.title}</h4>
      <div style={{
        background: "var(--bg-inset)",
        border: "1px solid var(--border-hair)",
        borderRadius: 7,
        padding: "10px 12px",
        fontFamily: "var(--font-mono)", fontSize: 11, color: "var(--fg-soft)",
        lineHeight: 1.55,
        maxHeight: 110,
        overflow: "hidden",
        whiteSpace: "pre-wrap",
      }}>{r.body}</div>
      <div className="row" style={{ gap: 4, flexWrap: "wrap" }}>
        {r.tags.slice(0, 5).map((t) => <span key={t} className="pill" style={{ fontSize: 10 }}>{t}</span>)}
      </div>
      <div className="row" style={{ justifyContent: "space-between", marginTop: 4 }}>
        <button className="k-btn k-btn--ghost" style={{ color: "var(--alert)", border: "none", padding: 0, fontSize: 11 }}>delete</button>
        <button className="k-btn" style={{ padding: "4px 10px" }}>open <window.Icons.chev /></button>
      </div>
    </div>
  );
}

window.PageRunbooks = PageRunbooks;
