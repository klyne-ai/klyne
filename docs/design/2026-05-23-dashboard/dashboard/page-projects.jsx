// Projects: merges /projects, /projects/[name], /worklog, /worklog/project.
// Layout: master list on the left, detail panel on the right with tabs
// (Overview · Sessions · Worklog · Files & Tools). Selecting a row updates the panel.
// Clicking a session opens the global SessionDrawer.

const { useState: useStatePj } = React;

function PageProjects({ onOpenSession }) {
  const projects = window.FX.projects;
  const [selectedId, setSelectedId] = useStatePj(projects[0].id);
  const [tab, setTab] = useStatePj("overview");
  const [filter, setFilter] = useStatePj("");
  const [cli, setCli] = useStatePj("all");
  const [sort, setSort] = useStatePj("recent");

  const filtered = projects
    .filter((p) => (cli === "all" ? true : p.clis.includes(cli)))
    .filter((p) => p.id.toLowerCase().includes(filter.toLowerCase()))
    .sort((a, b) => {
      if (sort === "sessions") return b.sessions - a.sessions;
      if (sort === "messages") return b.msgs - a.msgs;
      if (sort === "name") return a.id.localeCompare(b.id);
      return 0; // recent default
    });

  const sel = projects.find((p) => p.id === selectedId) || projects[0];

  return (
    <div>
      <header className="page-head">
        <div>
          <h1>Projects</h1>
          <p className="lede">
            Every project klyne has indexed under <span className="mono">~/.claude/projects/</span> and <span className="mono">~/.codex/sessions/</span>.
            Each card unifies its sessions, daily worklog reflections, token economics, and tooling usage in one view.
          </p>
        </div>
        <div className="actions">
          <span className="mono dim" style={{ fontSize: 11 }}>{filtered.length} of {projects.length}</span>
        </div>
      </header>

      <div className="toolbar">
        <div className="field" style={{ minWidth: 240 }}>
          <window.Icons.search />
          <input placeholder="Filter projects…" value={filter} onChange={(e) => setFilter(e.target.value)} />
        </div>
        <div className="field">
          <span className="lbl">cli</span>
          <select value={cli} onChange={(e) => setCli(e.target.value)}>
            <option value="all">all</option>
            <option value="claude">claude</option>
            <option value="codex">codex</option>
          </select>
        </div>
        <div className="field">
          <span className="lbl">sort</span>
          <select value={sort} onChange={(e) => setSort(e.target.value)}>
            <option value="recent">recent</option>
            <option value="sessions">sessions</option>
            <option value="messages">messages</option>
            <option value="name">name</option>
          </select>
        </div>
        <div className="grow" />
        <div className="row" style={{ gap: 16, fontSize: 11 }}>
          <span><span className="dot ok" style={{ display: "inline-block", marginRight: 6 }} /> fresh</span>
          <span><span className="dot warn" style={{ display: "inline-block", marginRight: 6 }} /> stale (new entries)</span>
          <span><span className="dot" style={{ display: "inline-block", marginRight: 6, background: "var(--fg-muted)" }} /> cold (no reflection)</span>
        </div>
      </div>

      <div style={{ display: "grid", gridTemplateColumns: "minmax(280px, 320px) minmax(0, 1fr)", gap: 18, alignItems: "start" }}>
        {/* LEFT: Project list */}
        <section className="card" style={{ overflow: "hidden" }}>
          <div style={{ padding: "10px 14px", borderBottom: "1px solid var(--border-hair)", display: "flex", alignItems: "center", justifyContent: "space-between" }}>
            <span className="kicker">{filtered.length} project{filtered.length === 1 ? "" : "s"}</span>
            <span className="mono dim" style={{ fontSize: 10.5 }}>sort: {sort}</span>
          </div>
          <div className="col" style={{ gap: 0, maxHeight: "70vh", overflow: "auto" }}>
            {filtered.map((p) => (
              <div key={p.id}
                onClick={() => setSelectedId(p.id)}
                style={{
                  padding: "12px 14px",
                  borderBottom: "1px solid var(--border-hair)",
                  cursor: "pointer",
                  background: p.id === selectedId ? "color-mix(in oklch, var(--accent) 7%, var(--bg-card))" : "transparent",
                  borderLeft: p.id === selectedId ? "2px solid var(--accent)" : "2px solid transparent",
                  display: "flex", flexDirection: "column", gap: 6,
                }}
              >
                <div className="row" style={{ justifyContent: "space-between", gap: 8 }}>
                  <div className="row" style={{ gap: 8, minWidth: 0, flex: 1 }}>
                    <span className={"dot " + (p.reflectionState === "fresh" ? "ok" : p.reflectionState === "stale" ? "warn" : "")} style={{ flexShrink: 0 }} />
                    <span style={{ color: "var(--fg)", fontWeight: 500, fontSize: 13, whiteSpace: "nowrap", overflow: "hidden", textOverflow: "ellipsis", minWidth: 0 }}>{p.id}</span>
                  </div>
                  <span className="mono dim" style={{ fontSize: 10.5, flexShrink: 0 }}>{p.lastAgo}</span>
                </div>
                <div className="row" style={{ justifyContent: "space-between", gap: 8 }}>
                  <div className="row" style={{ gap: 4 }}>
                    {p.clis.map((c) => (
                      <span key={c} className={"pill " + (c === "claude" ? "pill-claude" : "pill-codex")} style={{ fontSize: 10, padding: "1px 6px" }}>{c}</span>
                    ))}
                  </div>
                  <span className="mono dim" style={{ fontSize: 10.5 }}>{p.sessions} sessions · {p.tokensIn}</span>
                </div>
                {p.reflectionState !== "fresh" && (
                  <span className={"pill " + (p.reflectionState === "stale" ? "pill-warn" : "")} style={{ fontSize: 10, padding: "1px 6px", alignSelf: "flex-start", color: p.reflectionState === "cold" ? "var(--fg-muted)" : undefined }}>
                    {p.reflectionState === "stale" ? `${p.newSince} new since reflection` : "no reflection yet"}
                  </span>
                )}
              </div>
            ))}
          </div>
        </section>

        {/* RIGHT: Detail */}
        <section className="card">
          <ProjectDetailHeader p={sel} />
          <div style={{ padding: "0 18px" }}>
            <div className="tabs">
              {[
                ["overview", "Overview", null],
                ["sessions", "Sessions", sel.sessions],
                ["worklog", "Worklog", sel.id === "klyne" ? 2 : null],
                ["files", "Files", null],
              ].map(([id, label, n]) => (
                <div key={id} className={"tab" + (tab === id ? " active" : "")} onClick={() => setTab(id)}>
                  {label}{n != null && <span className="count">{n}</span>}
                </div>
              ))}
            </div>
          </div>
          <div style={{ padding: "16px 18px 20px" }}>
            {tab === "overview" && <ProjectOverview p={sel} onOpenSession={onOpenSession} />}
            {tab === "sessions" && <ProjectSessions p={sel} onOpenSession={onOpenSession} />}
            {tab === "worklog" && <ProjectWorklog p={sel} />}
            {tab === "files" && <ProjectFilesTools p={sel} />}
          </div>
        </section>
      </div>
    </div>
  );
}

function ProjectDetailHeader({ p }) {
  return (
    <div style={{ padding: "16px 18px", borderBottom: "1px solid var(--border-hair)" }}>
      <div className="row" style={{ justifyContent: "space-between", marginBottom: 6 }}>
        <div className="row" style={{ gap: 10 }}>
          <span className={"dot " + (p.reflectionState === "fresh" ? "ok" : p.reflectionState === "stale" ? "warn" : "")} />
          <h2 style={{ margin: 0, fontSize: 18, color: "var(--fg)", fontWeight: 500, letterSpacing: "-0.01em" }}>{p.id}</h2>
          {p.clis.map((c) => (
            <span key={c} className={"pill " + (c === "claude" ? "pill-claude" : "pill-codex")}>{c}</span>
          ))}
        </div>
        <button className="k-btn"><window.Icons.open /> Open in CLI</button>
      </div>
      <div className="mono dim" style={{ fontSize: 11 }}>{p.path}</div>
    </div>
  );
}

function ProjectOverview({ p, onOpenSession }) {
  const stats = [
    { k: "Sessions", v: p.sessions.toLocaleString(), s: "all-time" },
    { k: "Messages", v: p.msgs.toLocaleString(), s: `${p.tokensIn} in` },
    { k: "Tokens out", v: p.tokensOut, s: "cumulative" },
    { k: "Cost", v: p.cost, s: "no API key" },
  ];
  return (
    <div className="col" style={{ gap: 16 }}>
      <div style={{ display: "grid", gridTemplateColumns: "repeat(2, 1fr)", gap: 10 }}>
        {stats.map((s) => (
          <div key={s.k} className="stat">
            <span className="k">{s.k}</span>
            <span className="v">{s.v}</span>
            <span className="s">{s.s}</span>
          </div>
        ))}
      </div>

      <div className="card" style={{ padding: "14px 16px" }}>
        <div className="row" style={{ justifyContent: "space-between", marginBottom: 10 }}>
          <span className="kicker">Daily activity · 16d</span>
          <span className="mono dim" style={{ fontSize: 10.5 }}>tokens / day</span>
        </div>
        <DailyBars data={p.spark} />
      </div>

      <div className="card" style={{ padding: "14px 16px" }}>
        <div className="row" style={{ justifyContent: "space-between", marginBottom: 8 }}>
          <span className="kicker">Recent sessions · 3</span>
          <span className="mono dim" style={{ fontSize: 10.5 }}>see full list →</span>
        </div>
        <div className="col" style={{ gap: 4 }}>
          {window.FX.projectSessions.slice(0, 3).map((s) => (
            <SessionRowSlim key={s.id} s={s} onClick={() => onOpenSession(s.id)} />
          ))}
        </div>
      </div>
    </div>
  );
}

function DailyBars({ data }) {
  const max = Math.max(...data);
  return (
    <div className="row" style={{ alignItems: "flex-end", gap: 4, height: 60 }}>
      {data.map((v, i) => (
        <div key={i} style={{
          flex: 1,
          height: `${(v / max) * 100}%`,
          minHeight: 2,
          background: i === data.length - 1 ? "var(--accent)" : "color-mix(in oklch, var(--accent) 50%, var(--bg-inset))",
          borderRadius: 2,
        }} />
      ))}
    </div>
  );
}

function ProjectSessions({ p, onOpenSession }) {
  const sessions = window.FX.projectSessions;
  const grouped = sessions.reduce((acc, s) => {
    (acc[s.day] = acc[s.day] || []).push(s);
    return acc;
  }, {});
  return (
    <div className="col" style={{ gap: 14 }}>
      <div className="row" style={{ gap: 8 }}>
        {["all", "claude", "codex"].map((k, i) => (
          <button key={k} className={"k-btn" + (i === 0 ? " k-btn--active" : "")} style={{ padding: "4px 10px" }}>
            {k} <span className="dim" style={{ marginLeft: 4 }}>{i === 0 ? p.sessions : i === 1 ? p.sessions - 1 : 1}</span>
          </button>
        ))}
      </div>
      {Object.entries(grouped).map(([day, list]) => (
        <div key={day}>
          <div className="kicker" style={{ marginBottom: 8 }}>{day}</div>
          <div className="col" style={{ gap: 4 }}>
            {list.map((s) => <SessionRowSlim key={s.id} s={s} onClick={() => onOpenSession(s.id)} />)}
          </div>
        </div>
      ))}
    </div>
  );
}

function SessionRowSlim({ s, onClick }) {
  return (
    <div onClick={onClick} style={{
      display: "grid",
      gridTemplateColumns: "60px minmax(0, 1fr) 80px 70px 70px 16px",
      gap: 12,
      alignItems: "center",
      padding: "10px 12px",
      borderRadius: 8,
      background: "var(--bg-card-2)",
      border: "1px solid var(--border-hair)",
      cursor: "pointer",
    }}>
      <span className={"pill " + (s.cli === "claude" ? "pill-claude" : "pill-codex")} style={{ width: "fit-content" }}>{s.cli}</span>
      <div style={{ minWidth: 0 }}>
        <div style={{ fontSize: 12.5, color: "var(--fg)", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{s.title}</div>
        <div className="mono dim" style={{ fontSize: 10.5 }}>{s.short}</div>
      </div>
      <span className="mono dim right" style={{ fontSize: 11, textAlign: "right" }}>{s.msgs} msgs</span>
      <span className="mono dim right" style={{ fontSize: 11, textAlign: "right" }}>↓ {s.tokens}</span>
      <span className={"pill " + (s.state === "active" ? "pill-ok" : "")} style={{ justifySelf: "end" }}>{s.state}</span>
      <span style={{ color: "var(--fg-dim)" }}><window.Icons.chev /></span>
    </div>
  );
}

function ProjectWorklog({ p }) {
  if (p.id !== "klyne") {
    return (
      <div className="col" style={{ gap: 12 }}>
        <div className="kicker">No reflections yet for this project</div>
        <p className="muted" style={{ margin: 0, fontSize: 13 }}>
          Reflections are synthesized inside a CLI session — never by the klyne daemon. Run <span className="mono">/klyne:reflect</span> in this project to capture today.
        </p>
        <div className="card" style={{ padding: "10px 14px", display: "flex", alignItems: "center", justifyContent: "space-between" }}>
          <code className="mono" style={{ fontSize: 11.5, color: "var(--fg-soft)" }}>cd "{p.path}" && claude -p --permission-mode bypassPermissions '/klyne:reflect'</code>
          <button className="k-btn"><window.Icons.copy /> copy</button>
        </div>
      </div>
    );
  }
  const w = window.FX.worklog;
  return (
    <div className="col" style={{ gap: 14 }}>
      {w.state === "stale" && (
        <div className="card" style={{
          padding: "10px 14px",
          background: "color-mix(in oklch, var(--warn) 10%, var(--bg-card))",
          borderColor: "color-mix(in oklch, var(--warn) 30%, transparent)",
          display: "flex", alignItems: "center", justifyContent: "space-between",
        }}>
          <div className="row" style={{ gap: 10 }}>
            <span className="dot warn" />
            <span style={{ fontSize: 12.5 }}>
              <strong style={{ color: "var(--fg)" }}>{w.newSince} new entries</strong> since the last reflection — refresh by running:
            </span>
          </div>
          <button className="k-btn"><window.Icons.copy /> copy command</button>
        </div>
      )}
      {w.reflections.map((r) => (
        <div key={r.date} className="card" style={{ padding: "16px 18px" }}>
          <div className="row" style={{ justifyContent: "space-between", marginBottom: 10 }}>
            <h3 style={{ margin: 0, fontSize: 14, color: "var(--fg)", fontWeight: 500 }}>Daily reflection · {r.date}</h3>
            <span className="mono dim" style={{ fontSize: 10.5 }}>{r.evidence}</span>
          </div>
          <ul style={{ margin: 0, paddingLeft: 18, color: "var(--fg-soft)", fontSize: 12.5, lineHeight: 1.6 }}>
            {r.bullets.map((b, i) => <li key={i} style={{ marginBottom: 4 }}>{b}</li>)}
          </ul>
          {r.shipped.length > 0 && (
            <div style={{ marginTop: 10, paddingTop: 10, borderTop: "1px solid var(--border-hair)" }}>
              <div className="kicker" style={{ marginBottom: 6, color: "var(--ok)" }}>Shipped</div>
              {r.shipped.map((s, i) => (
                <div key={i} className="mono soft" style={{ fontSize: 11.5 }}>{s}</div>
              ))}
            </div>
          )}
        </div>
      ))}
    </div>
  );
}

function ProjectFilesTools({ p }) {
  const files = [
    ["ui/src/routes/productivity/+page.svelte", 142, "8h ago"],
    ["internal/worklog/reflection_recorder.go", 88, "1d ago"],
    ["docs/features/iterative-reflection.md", 64, "1d ago"],
    ["internal/store/migrations/020_reflection_stop_summary_cursor.sql", 41, "2d ago"],
    ["ui/src/routes/+layout.svelte", 31, "3d ago"],
  ];
  const tools = [
    ["Read", 2188], ["Bash", 1402], ["Edit", 998], ["Grep", 612], ["Write", 240], ["WebFetch", 84],
  ];
  return (
    <div className="col" style={{ gap: 14 }}>
      <div className="card" style={{ padding: "14px 16px" }}>
        <div className="kicker" style={{ marginBottom: 10 }}>Top files touched</div>
        <div className="col" style={{ gap: 6 }}>
          {files.map(([f, n, ago]) => (
            <div key={f} className="row" style={{ gap: 12 }}>
              <span className="mono soft" style={{ fontSize: 11.5, flex: 1, minWidth: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{f}</span>
              <span className="mono dim" style={{ fontSize: 10.5, width: 60, textAlign: "right" }}>{n}×</span>
              <span className="mono dim" style={{ fontSize: 10.5, width: 60, textAlign: "right" }}>{ago}</span>
            </div>
          ))}
        </div>
      </div>
      <div className="card" style={{ padding: "14px 16px" }}>
        <div className="kicker" style={{ marginBottom: 10 }}>Tools used</div>
        <div className="col" style={{ gap: 8 }}>
          {tools.map(([t, n]) => {
            const max = tools[0][1];
            return (
              <div key={t} className="row" style={{ gap: 12 }}>
                <span className="mono soft" style={{ fontSize: 12, width: 70 }}>{t}</span>
                <div className="hbar grow"><div className="fill" style={{ transform: `scaleX(${n / max})` }} /></div>
                <span className="mono dim" style={{ fontSize: 10.5, width: 50, textAlign: "right" }}>{n.toLocaleString()}</span>
              </div>
            );
          })}
        </div>
      </div>
    </div>
  );
}

window.PageProjects = PageProjects;
