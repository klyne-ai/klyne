// Insights: merges /insights + /stats into one analytics surface.
// Tabs at the top: Overview · Activity · Models · Projects.
// All metrics live in one URL — drilldowns go via project drawer / projects page.

const { useState: useStateIn } = React;

function PageInsights({ onJumpToProject }) {
  const [tab, setTab] = useStateIn("overview");
  const [window_, setWindow] = useStateIn("1d");
  const [cli, setCli] = useStateIn("both");

  const D = window.FX.insights;

  return (
    <div>
      <header className="page-head">
        <div>
          <h1>Insights</h1>
          <p className="lede">
            Cross-session token economics, model usage and activity rhythm — all derived locally from your JSONL via the cost engine.
            Movement and efficiency over inferred dollar amounts.
          </p>
        </div>
        <div className="actions">
          <div className="field" style={{ padding: "4px 8px" }}>
            <span className="lbl">cli</span>
            <select value={cli} onChange={(e) => setCli(e.target.value)}>
              <option>both</option><option>claude</option><option>codex</option>
            </select>
          </div>
          <div className="seg">
            {["1h","3h","6h","1d"].map(w => (
              <button key={w} className={window_ === w ? "on" : ""} onClick={() => setWindow(w)}>{w}</button>
            ))}
          </div>
          <div className="field" style={{ padding: "4px 8px" }}>
            <select
              value={["7d","30d","90d"].includes(window_) ? window_ : "more"}
              onChange={(e) => e.target.value !== "more" && setWindow(e.target.value)}
            >
              <option value="more" disabled>more…</option>
              <option value="7d">7 days</option>
              <option value="30d">30 days</option>
              <option value="90d">90 days</option>
            </select>
          </div>
        </div>
      </header>

      {/* Always-visible KPIs */}
      <div style={{ display: "grid", gridTemplateColumns: "repeat(6, minmax(0, 1fr))", gap: 10, marginBottom: 18 }}>
        <div className="stat"><span className="k">Spend</span><span className="v">{D.spend}</span><span className="s">subscription</span></div>
        <div className="stat"><span className="k">↑ Input</span><span className="v">{D.inputTokens}</span><span className="s">{D.cachedPct}</span></div>
        <div className="stat"><span className="k">↓ Output</span><span className="v">{D.outputTokens}</span><span className="s">all sessions</span></div>
        <div className="stat"><span className="k">Sessions</span><span className="v">{D.sessions.toLocaleString()}</span><span className="s">{window_}</span></div>
        <div className="stat"><span className="k">Messages</span><span className="v">{D.messages}</span><span className="s">turns</span></div>
        <div className="stat"><span className="k">Projects</span><span className="v">{D.projectsRanked.length}</span><span className="s">+ {window.FX.projects.length - D.projectsRanked.length} idle</span></div>
      </div>

      <div className="tabs" style={{ marginBottom: 18 }}>
        {[["overview","Overview"],["activity","Activity"],["models","Models"],["projects","Projects"]].map(([k, label]) => (
          <div key={k} className={"tab" + (tab === k ? " active" : "")} onClick={() => setTab(k)}>{label}</div>
        ))}
      </div>

      {tab === "overview" && <InsightsOverview D={D} />}
      {tab === "activity" && <InsightsActivity D={D} />}
      {tab === "models" && <InsightsModels D={D} />}
      {tab === "projects" && <InsightsProjects D={D} onJumpToProject={onJumpToProject} />}
    </div>
  );
}

function InsightsOverview({ D }) {
  return (
    <div className="col" style={{ gap: 18 }}>
      <div style={{ display: "grid", gridTemplateColumns: "minmax(0, 1.6fr) minmax(0, 1fr)", gap: 18 }}>
        <div className="card">
          <div className="card-head">
            <div className="title">Tokens per day <span className="sub">stacked · claude over codex</span></div>
            <div className="row" style={{ gap: 10 }}>
              <span className="row" style={{ gap: 4, fontSize: 11 }}><span style={{ width: 10, height: 6, background: "var(--accent)", borderRadius: 1 }} /><span className="mono dim">claude</span></span>
              <span className="row" style={{ gap: 4, fontSize: 11 }}><span style={{ width: 10, height: 6, background: "var(--info)", borderRadius: 1 }} /><span className="mono dim">codex</span></span>
            </div>
          </div>
          <div style={{ padding: 18 }}>
            <TokensPerDayChart data={D.perDay} labels={D.perDayLabels} />
          </div>
        </div>

        <div className="card">
          <div className="card-head">
            <div className="title">Agent mix <span className="sub">{D.agentMix.claudeTokens} + {D.agentMix.codexTokens}</span></div>
          </div>
          <div style={{ padding: 18 }}>
            <Donut claudePct={D.agentMix.claude} />
            <div className="col" style={{ gap: 8, marginTop: 14 }}>
              <MixRow color="var(--accent)" name="claude" pct={D.agentMix.claude} tokens={D.agentMix.claudeTokens} msgs={D.agentMix.claudeMsgs} />
              <MixRow color="var(--info)" name="codex" pct={D.agentMix.codex} tokens={D.agentMix.codexTokens} msgs={D.agentMix.codexMsgs} />
            </div>
          </div>
        </div>
      </div>

      <div className="card">
        <div className="card-head">
          <div className="title">Top projects by spend <span className="sub">5 of {window.FX.projects.length}</span></div>
          <a className="mono dim" style={{ fontSize: 11, cursor: "pointer" }}>see all →</a>
        </div>
        <div style={{ padding: "8px 0" }}>
          <ProjectRankTable rows={D.projectsRanked.slice(0, 5)} />
        </div>
      </div>
    </div>
  );
}

function MixRow({ color, name, pct, tokens, msgs }) {
  return (
    <div className="row" style={{ gap: 10 }}>
      <span style={{ width: 8, height: 8, background: color, borderRadius: 2 }} />
      <span className="mono" style={{ fontSize: 12, color: "var(--fg)" }}>{name}</span>
      <span className="mono dim" style={{ fontSize: 11 }}>{msgs}</span>
      <span className="grow" />
      <span className="mono" style={{ fontSize: 12, color: "var(--fg)" }}>{tokens}</span>
      <span className="mono dim" style={{ fontSize: 11, width: 40, textAlign: "right" }}>{pct}%</span>
    </div>
  );
}

function Donut({ claudePct }) {
  const r = 56, c = 2 * Math.PI * r;
  return (
    <div style={{ display: "flex", justifyContent: "center" }}>
      <svg width="160" height="160" viewBox="0 0 160 160">
        <circle cx="80" cy="80" r={r} fill="none" stroke="var(--info)" strokeWidth="18" />
        <circle cx="80" cy="80" r={r} fill="none" stroke="var(--accent)" strokeWidth="18"
          strokeDasharray={`${c * claudePct / 100} ${c}`} strokeDashoffset={c * 0.25} />
        <text x="80" y="78" textAnchor="middle" fontFamily="var(--font-mono)" fontSize="26" fill="var(--fg)" style={{ letterSpacing: "-0.02em" }}>{claudePct}%</text>
        <text x="80" y="96" textAnchor="middle" fontFamily="var(--font-mono)" fontSize="10" fill="var(--fg-dim)" style={{ letterSpacing: "0.14em" }}>CLAUDE</text>
      </svg>
    </div>
  );
}

function TokensPerDayChart({ data, labels }) {
  const max = Math.max(...data);
  const w = 700, h = 200, p = 6;
  const bw = (w - p * (data.length - 1)) / data.length;
  return (
    <div>
      <svg viewBox={`0 0 ${w} ${h + 26}`} width="100%" height={h + 26} preserveAspectRatio="none">
        {/* gridlines */}
        {[0.25, 0.5, 0.75].map(g => (
          <line key={g} x1="0" x2={w} y1={h - h*g} y2={h - h*g} stroke="var(--border-hair)" strokeDasharray="2 4" />
        ))}
        {data.map((v, i) => {
          const bh = (v / max) * (h - 8);
          const x = i * (bw + p);
          return <rect key={i} x={x} y={h - bh} width={bw} height={bh} fill="var(--accent)" rx="2" />;
        })}
        <text x="0" y={h + 18} fontFamily="var(--font-mono)" fontSize="11" fill="var(--fg-dim)">{labels[0]}</text>
        <text x={w} y={h + 18} textAnchor="end" fontFamily="var(--font-mono)" fontSize="11" fill="var(--fg-dim)">{labels[1]}</text>
      </svg>
    </div>
  );
}

function InsightsActivity({ D }) {
  const dayLabels = ["Sun","Mon","Tue","Wed","Thu","Fri","Sat"];
  const intensityColor = (n) => {
    if (n === 0) return "var(--bg-card-2)";
    return `color-mix(in oklch, var(--accent) ${n * 22}%, var(--bg-card))`;
  };
  return (
    <div className="col" style={{ gap: 18 }}>
      <div className="card">
        <div className="card-head">
          <div className="title">Activity heatmap <span className="sub">last 14 weeks</span></div>
          <div className="row" style={{ gap: 8, fontSize: 11 }}>
            <span className="mono dim">less</span>
            {[0,1,2,3,4].map(n => <span key={n} style={{ width: 12, height: 12, borderRadius: 2, background: intensityColor(n) }} />)}
            <span className="mono dim">more</span>
          </div>
        </div>
        <div style={{ padding: "20px 24px" }}>
          <div style={{ display: "grid", gridTemplateColumns: "auto 1fr", gap: 8 }}>
            <div className="col" style={{ gap: 4, marginTop: 2 }}>
              {dayLabels.map((d, i) => (
                <span key={d} className="mono dim" style={{ fontSize: 10, height: 14, lineHeight: "14px", visibility: i % 2 === 0 ? "visible" : "hidden" }}>{d}</span>
              ))}
            </div>
            <div style={{ display: "grid", gridTemplateColumns: `repeat(${D.heatmap[0].length}, 1fr)`, gap: 4 }}>
              {Array.from({ length: D.heatmap[0].length }).map((_, c) => (
                <div key={c} className="col" style={{ gap: 4 }}>
                  {D.heatmap.map((row, r) => (
                    <div key={r} style={{ width: "100%", aspectRatio: "1", borderRadius: 2, background: intensityColor(row[c]) }} />
                  ))}
                </div>
              ))}
            </div>
          </div>
        </div>
      </div>

      <div style={{ display: "grid", gridTemplateColumns: "repeat(4, 1fr)", gap: 10 }}>
        <div className="stat"><span className="k">Current streak</span><span className="v">{D.streak}<span className="muted" style={{ fontSize: 14 }}> days</span></span><span className="s">since May 9</span></div>
        <div className="stat"><span className="k">Longest streak</span><span className="v">{D.longestStreak}<span className="muted" style={{ fontSize: 14 }}> days</span></span><span className="s">Mar 12 – Apr 8</span></div>
        <div className="stat"><span className="k">Favorite model</span><span className="v mono" style={{ fontSize: 16 }}>{D.favoriteModel}</span><span className="s">{D.modelsByCost[0].pct}% spend share</span></div>
        <div className="stat"><span className="k">Peak hours</span><span className="v mono" style={{ fontSize: 16 }}>{D.peakHour}</span><span className="s">avg 38% of daily volume</span></div>
      </div>
    </div>
  );
}

function InsightsModels({ D }) {
  return (
    <div className="card">
      <div className="card-head">
        <div className="title">Models by cost <span className="sub">{D.modelsByCost.length} models · last 30d</span></div>
      </div>
      <table className="tbl">
        <thead>
          <tr>
            <th>Model</th>
            <th>Share</th>
            <th className="right">Sessions</th>
            <th className="right">Input</th>
            <th className="right">Output</th>
            <th className="right">Cache hit</th>
            <th className="right">Cost</th>
            <th className="right">%</th>
          </tr>
        </thead>
        <tbody>
          {D.modelsByCost.map((m, i) => (
            <tr key={m.name}>
              <td><span className="mono soft" style={{ fontSize: 12 }}>{m.name}</span></td>
              <td style={{ width: 220 }}>
                <div className="hbar"><div className="fill" style={{ transform: `scaleX(${m.pct / 100})`, background: i === 0 ? "var(--accent)" : "color-mix(in oklch, var(--accent) 60%, var(--bg-inset))" }} /></div>
              </td>
              <td className="num">{[4218, 412, 18, 0, 70][i] || "—"}</td>
              <td className="num">{["4.81B","178M","2.4M","—","12K"][i]}</td>
              <td className="num">{["37.2M","851K","42K","—","210"][i]}</td>
              <td className="num">{["96%","92%","88%","—","82%"][i]}</td>
              <td className="num">${m.cost.toFixed(2)}</td>
              <td className="num dim">{m.pct}%</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function InsightsProjects({ D, onJumpToProject }) {
  return (
    <div className="card">
      <div className="card-head">
        <div className="title">Projects ranked <span className="sub">by tokens · 30d · {D.projectsRanked.length} of {window.FX.projects.length}</span></div>
        <div className="seg">
          <button className="on">volume</button>
          <button>trend</button>
          <button>cache</button>
          <button>/compact</button>
        </div>
      </div>
      <div style={{ padding: "8px 0" }}>
        <ProjectRankTable rows={D.projectsRanked} onJumpToProject={onJumpToProject} />
      </div>
    </div>
  );
}

function ProjectRankTable({ rows, onJumpToProject }) {
  return (
    <table className="tbl">
      <thead>
        <tr>
          <th>Project</th>
          <th>Tokens (claude / codex)</th>
          <th className="right">Tokens</th>
          <th className="right">Trend</th>
          <th className="right">Cache hit</th>
          <th className="right">Tok / msg</th>
          <th className="right">/compact</th>
          <th></th>
        </tr>
      </thead>
      <tbody>
        {rows.map((p, i) => (
          <tr key={p.name} onClick={() => onJumpToProject && onJumpToProject(p.name)}>
            <td>
              <div className="row" style={{ gap: 8 }}>
                <span className={"dot " + (i === 0 ? "ok" : "")} />
                <span style={{ color: "var(--fg)" }}>{p.name}</span>
              </div>
            </td>
            <td style={{ width: 260 }}>
              <div className="hbar"><div className="fill" style={{ transform: `scaleX(${p.pct / 100})` }} /></div>
            </td>
            <td className="num">{p.tokens}</td>
            <td className="num" style={{ color: p.trend.startsWith("+") ? "var(--ok)" : p.trend.startsWith("-") ? "var(--alert)" : "var(--fg-dim)" }}>{p.trend}</td>
            <td className="num">{p.cacheHit}</td>
            <td className="num">{p.tokMsg}</td>
            <td className="num" style={{ color: "var(--warn)" }}>{p.compact}</td>
            <td className="right"><span className="mono dim" style={{ fontSize: 11 }}>open →</span></td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}

window.PageInsights = PageInsights;
