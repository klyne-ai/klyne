// Advisors: inbox of every advisory klyne has fired into your CLIs.
// Filterable by kind. Each row links into the offending session.

const { useState: useStateAd } = React;

function PageAdvisors({ onOpenSession }) {
  const A = window.FX.advisors;
  const counts = window.FX.advisorCounts;
  const [filter, setFilter] = useStateAd("all");
  const filtered = A.filter(a => filter === "all" ? true : a.kind === filter);

  return (
    <div>
      <header className="page-head">
        <div>
          <h1>Advisors</h1>
          <p className="lede">
            Every advisory klyne has fired across your CLI sessions. Each row is a real injection
            into your chat via the <span className="mono">UserPromptSubmit</span> hook — they were
            delivered to the AI's context at the moments shown, even if you didn't see them rendered
            visibly in the chat UI.
          </p>
        </div>
        <div className="actions">
          <span className="mono dim" style={{ fontSize: 11 }}>{counts.all} total · last 24h</span>
        </div>
      </header>

      <div className="row" style={{ gap: 8, marginBottom: 18, flexWrap: "wrap" }}>
        {[
          ["all", "all", counts.all, null],
          ["acceleration", "acceleration", counts.acceleration, "warn"],
          ["topic_shift", "topic shift", counts.topic_shift, "warn"],
          ["stale_context", "stale context", counts.stale_context, "warn"],
          ["hard_ceiling", "hard ceiling", counts.hard_ceiling, "alert"],
        ].map(([k, label, n, tone]) => (
          <button key={k}
            className={"k-btn" + (filter === k ? " k-btn--active" : "")}
            onClick={() => setFilter(k)}
            style={{ padding: "6px 12px" }}
          >
            {tone && <span className={"dot " + tone}></span>}
            {label} <span className="dim" style={{ marginLeft: 6 }}>{n}</span>
          </button>
        ))}
      </div>

      <div className="col" style={{ gap: 10 }}>
        {filtered.map((a, i) => {
          const toneVar = a.level === "alert" ? "var(--alert)" : a.level === "warn" ? "var(--warn)" : "var(--info)";
          return (
            <div key={i}
              className="card"
              style={{
                padding: "14px 18px",
                borderLeft: `3px solid ${toneVar}`,
                cursor: "pointer",
                display: "flex", flexDirection: "column", gap: 8,
              }}
              onClick={() => onOpenSession(a.session)}
            >
              <div className="row" style={{ justifyContent: "space-between", gap: 12 }}>
                <div className="row" style={{ gap: 12, minWidth: 0 }}>
                  <span className="kicker" style={{ color: toneVar, fontSize: 10.5 }}>{a.kind.replace(/_/g, " ")}</span>
                  <span className="mono dim" style={{ fontSize: 11 }}>claude</span>
                  <span className="mono dim" style={{ fontSize: 11 }}>{a.session}</span>
                  <span className="mono dim" style={{ fontSize: 11 }}>in {a.project}</span>
                </div>
                <span className="mono dim" style={{ fontSize: 11, flexShrink: 0 }}>{a.ago} ago</span>
              </div>
              <p style={{ margin: 0, fontSize: 12.75, color: "var(--fg-soft)", lineHeight: 1.55 }}>{a.body}</p>
            </div>
          );
        })}
      </div>
    </div>
  );
}

window.PageAdvisors = PageAdvisors;
