// Worklog: per-project reflection rollup. Each card shows the latest
// synthesized reflection (written by /klyne:reflect) + state badge:
//   stale  — reflection exists but N new visible entries arrived after it
//   cold   — no reflection yet, entries waiting
//   fresh  — covered
// Stale/cold cards surface a copy-able /klyne:reflect command. UI never
// triggers AI synthesis itself — that's the user's CLI session's job.

const { useState: useStateWl } = React;

function PageWorklog() {
  const W = window.FX.worklogProjects;
  const [filter, setFilter] = useStateWl("all");
  const filtered = W.filter(w => filter === "all" ? true : w.state === filter);
  const counts = {
    all: W.length,
    stale: W.filter(w => w.state === "stale").length,
    cold: W.filter(w => w.state === "cold").length,
    fresh: W.filter(w => w.state === "fresh").length,
  };

  return (
    <div>
      <header className="page-head">
        <div>
          <h1>Worklog</h1>
          <p className="lede">
            Per-project reflection rollup. Each card shows the latest synthesized reflection written by <span className="mono">/klyne:reflect</span>.
            When new sessions land on top of the last reflection, the project flags stale — copy the command and run it in the project to refresh.
            The daemon never invokes the LM itself.
          </p>
        </div>
        <div className="actions">
          <span className="mono dim" style={{ fontSize: 11 }}>{W.length} projects</span>
        </div>
      </header>

      <div className="row" style={{ gap: 8, marginBottom: 16, flexWrap: "wrap" }}>
        {[
          ["all", "all", counts.all, null],
          ["stale", "stale", counts.stale, "warn"],
          ["cold", "cold", counts.cold, null],
          ["fresh", "fresh", counts.fresh, "ok"],
        ].map(([k, label, n, tone]) => (
          <button key={k}
            className={"k-btn" + (filter === k ? " k-btn--active" : "")}
            onClick={() => setFilter(k)}
            style={{ padding: "4px 10px" }}
          >
            {tone && <span className={"dot " + tone}></span>}
            {label} <span className="dim" style={{ marginLeft: 4 }}>{n}</span>
          </button>
        ))}
      </div>

      <div className="col" style={{ gap: 14 }}>
        {filtered.map((w) => <WorklogCard key={w.project} w={w} />)}
      </div>
    </div>
  );
}

function WorklogCard({ w }) {
  const tone =
    w.state === "stale" ? "var(--warn)" :
    w.state === "cold"  ? "var(--fg-muted)" :
                          "var(--ok)";
  const stateBadge =
    w.state === "stale" ? <span className="pill pill-warn">{w.newSince} new entries since reflection</span> :
    w.state === "cold"  ? <span className="pill">{w.newSince} entr{w.newSince === 1 ? "y" : "ies"} · no reflection yet</span> :
                          <span className="pill pill-ok">fresh</span>;

  const [running, setRunning] = useStateWl(false);
  const [elapsed, setElapsed] = useStateWl(0);
  React.useEffect(() => {
    if (!running) return;
    setElapsed(0);
    const id = setInterval(() => setElapsed((e) => e + 1), 1000);
    return () => clearInterval(id);
  }, [running]);

  return (
    <div className="card" style={{
      padding: 0, overflow: "hidden",
      borderLeft: `3px solid ${tone}`,
    }}>
      {/* Header */}
      <div style={{ padding: "14px 18px", display: "flex", alignItems: "flex-start", justifyContent: "space-between", gap: 12, borderBottom: w.latest ? "1px solid var(--border-hair)" : "none" }}>
        <div style={{ minWidth: 0 }}>
          <div className="row" style={{ gap: 10, marginBottom: 4 }}>
            <h3 style={{ margin: 0, fontSize: 16, color: "var(--fg)", fontWeight: 500, letterSpacing: "-0.01em" }}>{w.project}</h3>
            {stateBadge}
          </div>
          <div className="mono dim" style={{ fontSize: 11 }}>
            {w.reflectedAgo ? `reflected ${w.reflectedAgo} ago` : "never reflected"} · last activity {w.lastActivityAgo} ago
          </div>
          <div className="mono" style={{ fontSize: 11, color: "var(--fg-muted)", marginTop: 4 }}>{w.path}</div>
        </div>
      </div>

      {/* Latest reflection (if any) */}
      {w.latest && (
        <div style={{ padding: "16px 18px", display: "flex", flexDirection: "column", gap: 12 }}>
          <div className="row" style={{ justifyContent: "space-between" }}>
            <h4 style={{ margin: 0, fontSize: 13.5, color: "var(--fg)", fontWeight: 500 }}>
              Daily reflection · <span className="mono">{w.latest.date}</span>
            </h4>
            <span className="mono dim" style={{ fontSize: 10.5 }}>{w.latest.evidence}</span>
          </div>
          <div style={{
            background: "var(--bg-inset)",
            border: "1px solid var(--border-hair)",
            borderRadius: 8,
            padding: "14px 16px",
            fontSize: 12.5, color: "var(--fg-soft)", lineHeight: 1.6,
          }}>
            <ul style={{ margin: 0, paddingLeft: 18 }}>
              {w.latest.bullets.map((b, i) => <li key={i} style={{ marginBottom: 6 }}>{b}</li>)}
            </ul>
            {(w.latest.shipped.length > 0 || w.latest.openLoops.length > 0) && (
              <div style={{ marginTop: 14, paddingTop: 14, borderTop: "1px dashed var(--border-soft)", display: "flex", flexDirection: "column", gap: 10 }}>
                {w.latest.shipped.length > 0 && (
                  <div>
                    <div className="kicker" style={{ color: "var(--ok)", marginBottom: 6 }}>Shipped</div>
                    {w.latest.shipped.map((s, i) => <div key={i} className="mono" style={{ fontSize: 11.5, color: "var(--fg-soft)", lineHeight: 1.55 }}>· {s}</div>)}
                  </div>
                )}
                {w.latest.openLoops.length > 0 && (
                  <div>
                    <div className="kicker" style={{ color: "var(--warn)", marginBottom: 6 }}>Open loops</div>
                    {w.latest.openLoops.map((s, i) => <div key={i} className="mono" style={{ fontSize: 11.5, color: "var(--fg-soft)", lineHeight: 1.55 }}>· {s}</div>)}
                  </div>
                )}
              </div>
            )}
          </div>
        </div>
      )}

      {/* Refresh / Run command (always shown for stale/cold) */}
      {w.state !== "fresh" && (
        <div style={{
          padding: "12px 18px",
          borderTop: w.latest ? "1px dashed var(--border-soft)" : "none",
          display: "flex", flexDirection: "column", gap: 8,
        }}>
          <div className="kicker">
            {w.state === "cold" ? "Generate the first reflection:" : "Refresh with the latest entries:"}
          </div>
          <div className="row" style={{ gap: 8 }}>
            <code className="mono" style={{
              flex: 1, minWidth: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap",
              background: "var(--bg-inset)",
              border: "1px solid var(--border-hair)",
              borderRadius: 6,
              padding: "8px 12px",
              fontSize: 11.5, color: "var(--fg-soft)",
            }}>{w.refreshCmd}</code>
            <button className="k-btn"><window.Icons.copy /> copy</button>
            {running ? (
              <button
                className="k-btn"
                onClick={() => setRunning(false)}
                style={{
                  background: "color-mix(in oklch, var(--alert) 18%, var(--bg-card-2))",
                  borderColor: "color-mix(in oklch, var(--alert) 45%, transparent)",
                  color: "var(--alert)",
                }}
              >
                ✖ stop
              </button>
            ) : (
              <button
                className="k-btn"
                onClick={() => setRunning(true)}
                style={{
                  background: "color-mix(in oklch, var(--info) 14%, var(--bg-card-2))",
                  borderColor: "color-mix(in oklch, var(--info) 40%, transparent)",
                  color: "var(--info)",
                }}
              >
                ▶ run
              </button>
            )}
          </div>

          {/* Running readout */}
          {running && (
            <div style={{
              background: "var(--bg-inset)",
              border: "1px solid color-mix(in oklch, var(--warn) 35%, var(--border-hair))",
              borderLeft: "3px solid var(--warn)",
              borderRadius: 8,
              padding: "12px 14px",
              marginTop: 4,
              display: "flex", flexDirection: "column", gap: 8,
            }}>
              <div className="row" style={{ gap: 8 }}>
                <span className="mono" style={{ color: "var(--warn)", fontSize: 12 }}>
                  <SpinnerDots /> Running on the daemon
                </span>
                <span className="mono dim" style={{ fontSize: 11 }}>· {elapsed}s elapsed</span>
              </div>
              <div className="mono" style={{
                background: "var(--bg)",
                border: "1px solid var(--border-hair)",
                borderRadius: 6,
                padding: "8px 12px",
                fontSize: 11, color: "var(--fg-soft)",
                whiteSpace: "nowrap", overflow: "hidden", textOverflow: "ellipsis",
              }}>
                <span style={{ color: "var(--fg-dim)" }}>$ </span>{w.refreshCmd}
              </div>
              <div className="mono dim" style={{ fontSize: 10.5, lineHeight: 1.55 }}>
                Hit <span style={{ color: "var(--alert)" }}>✖ stop</span> to kill the subprocess (SIGKILL via context cancel). A 5-minute server-side timeout also applies.
              </div>
            </div>
          )}
        </div>
      )}
    </div>
  );
}

function SpinnerDots() {
  const [tick, setTick] = useStateWl(0);
  React.useEffect(() => {
    const id = setInterval(() => setTick((t) => (t + 1) % 4), 250);
    return () => clearInterval(id);
  }, []);
  return <span style={{ display: "inline-block", width: 10, textAlign: "left" }}>{".".repeat(tick) + " ".repeat(3 - tick)}</span>;
}

window.PageWorklog = PageWorklog;
