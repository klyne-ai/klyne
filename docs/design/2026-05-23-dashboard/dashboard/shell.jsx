// App shell: left sidebar nav + top bar + global search palette.
// Pages are routed in app.jsx; shell only emits nav events.

const { useState, useEffect, useCallback } = React;

function Sidebar({ current, onNavigate, onSearch, live, collapsed, onToggleCollapse }) {
  const items = [
    { id: "live",         label: "Live",         icon: "live",         meta: live > 0 ? `${live} live` : null, metaKind: live > 0 ? "live" : null },
    { id: "productivity", label: "Productivity", icon: "productivity", meta: "today", metaKind: null },
    { id: "projects",     label: "Projects",     icon: "projects",     meta: "20", metaKind: null },
    { id: "insights",     label: "Insights",     icon: "insights",     meta: "1d", metaKind: null },
    { id: "runbooks",     label: "Runbooks",     icon: "runbooks",     meta: "5", metaKind: null },
  ];
  const capture = [
    { id: "advisors", label: "Advisors", icon: "flame",  meta: "95", metaKind: "alert" },
    { id: "worklog",  label: "Worklog",  icon: "branch", meta: "9",  metaKind: null },
  ];
  return (
    <aside className="sidebar">
      <div className="sidebar-brand">
        <div className="mark">&gt;K</div>
        <div className="name">klyne<em>·local</em></div>
        <button className="sidebar-toggle" onClick={onToggleCollapse} title={collapsed ? "Expand" : "Collapse"}>
          {collapsed ? "›" : "‹"}
        </button>
      </div>

      <div className="sidebar-search" onClick={onSearch} title="Search messages…">
        <window.Icons.search />
        <span>Search messages…</span>
        <span className="key">⌘K</span>
      </div>

      <div className="sidebar-section">Workspace</div>
      <nav className="sidebar-nav">
        {items.map((it) => {
          const I = window.Icons[it.icon];
          return (
            <div key={it.id}
              className={"sidebar-nav-item" + (current === it.id ? " active" : "")}
              onClick={() => onNavigate(it.id)}
              title={collapsed ? it.label : undefined}
            >
              <span className="icon"><I /></span>
              <span className="label">{it.label}</span>
              {it.meta && <span className={"meta" + (it.metaKind ? " " + it.metaKind : "")}>{it.meta}</span>}
            </div>
          );
        })}
      </nav>

      <div className="sidebar-section">Capture</div>
      <div className="sidebar-nav">
        {capture.map((it) => {
          const I = window.Icons[it.icon];
          return (
            <div key={it.id}
              className={"sidebar-nav-item" + (current === it.id ? " active" : "")}
              onClick={() => onNavigate(it.id)}
              title={collapsed ? it.label : undefined}
            >
              <span className="icon"><I /></span>
              <span className="label">{it.label}</span>
              {it.meta && <span className={"meta" + (it.metaKind ? " " + it.metaKind : "")}>{it.meta}</span>}
            </div>
          );
        })}
      </div>

      <div className="sidebar-foot">
        <div className="avatar">M</div>
        <div className="who">
          <div className="name">Mohit Patel</div>
          <div className="sub">127.0.0.1:7878</div>
        </div>
        <div className="status" title="daemon running"></div>
      </div>
    </aside>
  );
}

function Topbar({ crumbs, right, status }) {
  return (
    <div className="topbar">
      <div className="crumbs">
        {crumbs.map((c, i) => (
          <span key={i} className={i === crumbs.length - 1 ? "here" : ""}>
            {c}
            {i < crumbs.length - 1 && <span className="sep" style={{margin: "0 8px"}}>/</span>}
          </span>
        ))}
      </div>
      <div className="grow" />
      {right}
      {status && (
        <div className="meta">
          <span className="status-pill"><span className="dot" />daemon · {status}</span>
        </div>
      )}
    </div>
  );
}

function SearchPalette({ onClose, onPick }) {
  const [q, setQ] = useState("");
  useEffect(() => {
    const onKey = (e) => {
      if (e.key === "Escape") onClose();
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [onClose]);

  const filtered = window.FX.searchSuggest.filter((s) => s.includes(q.toLowerCase()));

  // Mock recent hits for demo
  const hits = q.length > 0 ? [
    { kind: "msg", project: "ops-app", session: "ef03ea3", text: `…fix the ${q} race in the payment-followup webhook before re-running…`, ago: "16h" },
    { kind: "session", project: "klyne", session: "191eef49", text: `KLYNE_SUMMARY: ${q} flow refactored, importance 8`, ago: "1d" },
    { kind: "runbook", project: null, session: null, text: `runbook · "${q}" handling — rollback playbook`, ago: "3d" },
  ] : [];

  return (
    <div className="palette-scrim" onClick={onClose}>
      <div className="palette" onClick={(e) => e.stopPropagation()}>
        <input
          autoFocus
          placeholder="Search messages, sessions, projects…"
          value={q}
          onChange={(e) => setQ(e.target.value)}
        />
        {q.length > 0 ? (
          <div className="group">
            <div className="group-label">{hits.length} hits · FTS5</div>
            {hits.map((h, i) => (
              <div key={i} className="row" onClick={() => { onPick(h); onClose(); }}>
                <span className="icon">
                  {h.kind === "msg" && <window.Icons.search />}
                  {h.kind === "session" && <window.Icons.live />}
                  {h.kind === "runbook" && <window.Icons.runbooks />}
                </span>
                <div>
                  <div className="label">{h.text}</div>
                  <div className="desc">{[h.project, h.session, h.ago].filter(Boolean).join(" · ")}</div>
                </div>
                <window.Icons.open />
              </div>
            ))}
          </div>
        ) : (
          <>
            <div className="group">
              <div className="group-label">Try</div>
              {filtered.map((s) => (
                <div key={s} className="row" onClick={() => setQ(s)}>
                  <span className="icon"><window.Icons.search /></span>
                  <div><div className="label">{s}</div></div>
                  <span className="desc">filter</span>
                </div>
              ))}
            </div>
            <div className="group">
              <div className="group-label">Jump to</div>
              {[
                ["Productivity / today", "productivity"],
                ["Projects / klyne", "projects"],
                ["Runbooks", "runbooks"],
              ].map(([label, id]) => (
                <div key={label} className="row" onClick={() => { onPick({ jump: id }); onClose(); }}>
                  <span className="icon"><window.Icons.chev /></span>
                  <div><div className="label">{label}</div></div>
                  <span className="desc">enter</span>
                </div>
              ))}
            </div>
          </>
        )}
      </div>
    </div>
  );
}

Object.assign(window, { Sidebar, Topbar, SearchPalette });
