// App router. Tracks current page, session drawer, search palette.
// All state is local (URL hash mirroring would be a nice next step).

const { useState: useStateApp, useEffect: useEffectApp, useRef: useRefApp } = React;

function App() {
  const [page, setPage] = useStateApp(() => {
    const hash = location.hash.replace("#", "");
    if (["live","productivity","projects","insights","runbooks","worklog","advisors"].includes(hash)) return hash;
    return "live";
  });
  const [sessionId, setSessionId] = useStateApp(null);
  const [paletteOpen, setPaletteOpen] = useStateApp(false);
  const [collapsed, setCollapsed] = useStateApp(page === "live");
  const userToggledRef = useRefApp(false);

  // Hash routing
  useEffectApp(() => {
    location.hash = page;
  }, [page]);

  // Auto-collapse on Live, auto-expand off Live — unless the user has manually toggled.
  useEffectApp(() => {
    if (userToggledRef.current) return;
    setCollapsed(page === "live");
  }, [page]);

  const handleToggleCollapse = () => {
    userToggledRef.current = true;
    setCollapsed((c) => !c);
  };

  // ⌘K to open search
  useEffectApp(() => {
    const onKey = (e) => {
      if ((e.metaKey || e.ctrlKey) && e.key === "k") {
        e.preventDefault();
        setPaletteOpen(true);
      }
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, []);

  const liveCount = window.FX.liveSessions.filter(s => s.state === "live").length;

  const crumbs = {
    live: ["Workspace", "Live"],
    productivity: ["Workspace", "Productivity"],
    projects: ["Workspace", "Projects"],
    insights: ["Workspace", "Insights"],
    runbooks: ["Workspace", "Runbooks"],
    worklog: ["Workspace", "Worklog"],
    advisors: ["Capture", "Advisors"],
  }[page];

  return (
    <div className={"app" + (collapsed ? " collapsed" : "")} data-screen-label={`klyne · ${page}`}>
      <window.Sidebar
        current={page}
        onNavigate={setPage}
        onSearch={() => setPaletteOpen(true)}
        live={liveCount}
        collapsed={collapsed}
        onToggleCollapse={handleToggleCollapse}
      />
      <div className="main">
        <window.Topbar
          crumbs={crumbs}
          status="running · 127.0.0.1:7878"
          right={
            page === "live"
              ? <span className="mono" style={{ color: "var(--ok)", fontSize: 11, whiteSpace: "nowrap" }}>{liveCount} live · streaming</span>
              : null
          }
        />
        <div className="page">
          {page === "live" && <window.PageLive onOpenSession={setSessionId} />}
          {page === "productivity" && <window.PageProductivity />}
          {page === "projects" && <window.PageProjects onOpenSession={setSessionId} />}
          {page === "insights" && <window.PageInsights onJumpToProject={() => setPage("projects")} />}
          {page === "runbooks" && <window.PageRunbooks />}
          {page === "worklog" && <window.PageWorklog />}
          {page === "advisors" && <window.PageAdvisors onOpenSession={setSessionId} />}
        </div>
      </div>
      {sessionId && <window.SessionDrawer id={sessionId} onClose={() => setSessionId(null)} />}
      {paletteOpen && (
        <window.SearchPalette
          onClose={() => setPaletteOpen(false)}
          onPick={(h) => {
            if (h.jump) setPage(h.jump);
            else if (h.session) setSessionId(h.session);
          }}
        />
      )}
    </div>
  );
}

ReactDOM.createRoot(document.getElementById("root")).render(<App />);
