// Extended fixtures for the unified dashboard.
// All data is plausibly derived from the screenshots — keeps numbers consistent.

const FX = {
  // Live cockpit/work sessions
  liveSessions: [
    {
      id: "7a61a34", project: "klyne", cli: "claude", branch: "main", state: "live",
      lastAgo: "0s", msgs: 7, tokens: "14K", ctxPct: 22,
      tail: [
        { role: "claude", t: "1m", text: "Daemon is already running on port 7878. Let me create the screenshots directory and find dynamic IDs for the [name] / [id] routes." },
        { role: "claude", t: "1m", text: "I have all the URLs I need. Now I'll resize the viewport and screenshot each page." },
        { role: "claude", t: "42s", text: "The Playwright MCP browser is locked by a stale Chrome process (PID 35351) holding the user-data-dir." },
        { role: "claude", t: "11s", text: "Lock is released. Now let me drive the browser through each page." },
      ],
    },
    {
      id: "ef03ea3", project: "ops-app", cli: "claude", branch: "feature/TICKET-1452-skip-payment-followup-tele", state: "idle",
      lastAgo: "2m", msgs: 123, tokens: "81K", ctxPct: 64,
      tail: [
        { role: "claude", t: "16h", text: "Got it — separate flag. Since the backend currently triggers auto-discount on reason='Follow Up', we'll either coordinate a backend change or still send reason='Follow Up' under the hood when toggle is ON." },
        { role: "claude", t: "16h", text: "Let me set up tracking for the brainstorming checklist." },
        { role: "claude", t: "16h", text: "Quickly checking the existing visits API to inform the next question." },
        { role: "claude", t: "16h", text: "Understood — toggle is tele-only. (That clarifies the previous answer.)" },
      ],
    },
    {
      id: "9b18dc1", project: "consult-service", cli: "codex", branch: "feat/intake-form-v2", state: "live",
      lastAgo: "12s", msgs: 41, tokens: "32K", ctxPct: 38,
      tail: [
        { role: "codex", t: "12s", text: "Wired the new intake schema to /api/intake/v2 — running the contract tests now." },
        { role: "codex", t: "1m", text: "Three fields are still optional that should be required: dob, primary_phone, consent_at." },
        { role: "codex", t: "3m", text: "Reading IntakeForm.svelte — there's a stale `triage_v1` import we can drop." },
      ],
    },
    {
      id: "c4422f0", project: "product-svc", cli: "claude", branch: "feature/labstack-rollout", state: "live",
      lastAgo: "3s", msgs: 88, tokens: "62K", ctxPct: 71,
      tail: [
        { role: "claude", t: "3s", text: "Ready to coordinate the labstack rollout across all four projects. Should I proceed in product-svc first, then ops-app, then customer-svc?" },
        { role: "claude", t: "30s", text: "I see 12 references to the old labstack v1 client in product-svc. Migration plan: 1) bump lab-client to v2.1.0, 2) swap LabReporter usage, 3) update three integration tests." },
        { role: "claude", t: "1m", text: "Checked ops-app — it already uses v2 of the client. So we only need to bump product-svc, consult-service, and customer-svc to match." },
      ],
    },
    {
      id: "0bd910c", project: "customer-svc", cli: "claude", branch: "main", state: "idle",
      lastAgo: "16m", msgs: 218, tokens: "87K", ctxPct: 45,
      tail: [
        { role: "claude", t: "16m", text: "Pending: still waiting on your call on whether to bundle the migration with the labstack bump or ship them separately." },
        { role: "claude", t: "18m", text: "I've staged the bump but haven't run it yet — your last message said \"hold on, let me check the lab tag situation first\". Standing by." },
      ],
    },
    {
      id: "1ba92bc", project: "orders-service", cli: "codex", branch: "fix/inventory-sync-race", state: "idle",
      lastAgo: "1h", msgs: 27, tokens: "13K", ctxPct: 14,
      tail: [
        { role: "codex", t: "1h", text: "The race in inventory-sync looks like a missing lock around the receipt-batch flush. Adding sync.Mutex on InventoryStore.flush() should resolve it; need your sign-off before I push." },
        { role: "codex", t: "1h", text: "I've drafted the unit test that reproduces it (3 of 10 runs fail without the fix, 0 of 10 with). Want me to add it to the PR or skip and go straight to the fix?" },
      ],
    },
  ],

  advisors: [
    { kind: "stale_context", level: "warn", session: "191eef49", project: "klyne", ago: "17h", body: "klyne: ~53% of loaded file context is stale relative to your current direction. Files still relevant: +page.svelte, productivity.go, types.go. Run /klyne:handoff scope=current to carry forward only those." },
    { kind: "acceleration", level: "warn", session: "191eef49", project: "klyne", ago: "18h", body: "klyne: per-turn cost has roughly doubled (latest turn ~6K uncached). Continuing here will burn through your 5-hour window faster than starting fresh — /klyne:handoff scope=current keeps the relevant context." },
    { kind: "hard_ceiling", level: "alert", session: "191eef49", project: "klyne", ago: "22h", body: "klyne: this session is 76% full — the next turn's prefix will keep growing. Run /klyne:handoff scope=current and start fresh." },
    { kind: "topic_shift", level: "warn", session: "191eef49", project: "klyne", ago: "1d", body: "klyne: your prompts have shifted topic since the session opened — about 73% of loaded files are stale relative to your new direction. Files still relevant: README.md, types.go, +page.svelte." },
    { kind: "stale_context", level: "warn", session: "67ad9b21", project: "ops-app", ago: "1d", body: "klyne: 41% of loaded file context is stale. Recent prompts focus on /billing/ but loaded context skews to /onboarding/. Consider /klyne:handoff scope=current." },
    { kind: "five_hour", level: "info", session: "0bd910c4", project: "consult-service", ago: "2d", body: "klyne: you've been in this session ~4h 40m of a 5-hour cap — consider wrapping or running /klyne:handoff to preserve state." },
  ],

  advisorCounts: { all: 95, acceleration: 40, topic_shift: 29, stale_context: 24, hard_ceiling: 2 },

  // Top-level project index (Projects + Worklog merged)
  projects: [
    { id: "klyne", path: "/Users/you/Desktop/Project/klyne", clis: ["claude","codex"], sessions: 313, msgs: 4799, tokensIn: "534M", tokensOut: "3.3M", cost: "—", lastAgo: "0s", reflectionState: "stale", newSince: 3, spark: [4,6,8,12,14,11,18,22,28,34,30,40,52,48,55,60] },
    { id: "ops-app", path: "/Users/you/Desktop/Learning/ops-app", clis: ["claude"], sessions: 13, msgs: 1943, tokensIn: "667K", tokensOut: "14K", cost: "—", lastAgo: "3m", reflectionState: "fresh", newSince: 0, spark: [2,4,8,16,18,15,12,9,18,25,22,30,28,34,40,38] },
    { id: "consult-service", path: "/Users/you/code/consult-service", clis: ["claude","codex"], sessions: 5, msgs: 135, tokensIn: "19K", tokensOut: "1K", cost: "—", lastAgo: "20h", reflectionState: "fresh", newSince: 0, spark: [1,1,2,1,2,3,4,3,3,5,6,5,4,7,6,8] },
    { id: "customer-svc", path: "/Users/you/code/customer-svc", clis: ["claude"], sessions: 1, msgs: 218, tokensIn: "87K", tokensOut: "2K", cost: "—", lastAgo: "16h", reflectionState: "cold", newSince: 1, spark: [0,0,1,0,1,0,1,0,2,3,3,2,4,3,4,5] },
    { id: "trackIt", path: "/Users/you/Desktop/trackIt", clis: ["claude"], sessions: 1, msgs: 27, tokensIn: "7K", tokensOut: "200", cost: "—", lastAgo: "1d", reflectionState: "stale", newSince: 4, spark: [0,1,0,1,2,3,2,1,2,3,4,3,2,3,4,3] },
    { id: "orders-service", path: "/Users/you/code/orders-service", clis: ["codex"], sessions: 4, msgs: 87, tokensIn: "44K", tokensOut: "2K", cost: "—", lastAgo: "5h", reflectionState: "fresh", newSince: 0, spark: [1,2,3,5,4,3,2,4,3,2,1,2,3,2,1,2] },
    { id: "product-svc", path: "/Users/you/code/product-svc", clis: ["claude"], sessions: 8, msgs: 412, tokensIn: "98K", tokensOut: "4K", cost: "—", lastAgo: "1d", reflectionState: "cold", newSince: 8, spark: [0,0,0,1,2,4,6,8,10,12,11,14,16,18,15,14] },
  ],

  // Sessions for the selected project's drill-in
  projectSessions: [
    { id: "d7a61a34-f431-4112-8166-8a2b8f9fa03a", short: "d7a61a34", cli: "claude", title: "Loading recent activity…", state: "active", msgs: 127, tokens: "16K", day: "Today" },
    { id: "bd3e7f78-95a4-4b95-9a17-92858076d3d1", short: "bd3e7f78", cli: "claude", title: "All green: `go vet` clean, all 6 reflect-run tests + worklog/contracts tests pass, klyne binary builds at 21 M…", state: "idle", msgs: 298, tokens: "101K", day: "Yesterday" },
    { id: "191eef49-d629-4868-999a-29a97717f25d", short: "191eef49", cli: "claude", title: "You're conflating two different auth paths. They look like \"the same GitHub\" but they use **completely sep…", state: "idle", msgs: 2268, tokens: "1.7M", day: "Yesterday" },
    { id: "1ba92bcc-c23c-430f-9a7a-449f1d471339", short: "1ba92bcc", cli: "claude", title: "Wrote 1 daily reflection: **2026-05-22** → `ref-2026-05-22-1779449861067204000` (4 insights, all citin…", state: "idle", msgs: 27, tokens: "13K", day: "Yesterday" },
    { id: "89d0fe64-cfb0-4d3e-a3b9-c22fbff2b50b", short: "89d0fe64", cli: "claude", title: "Wrote 2 daily reflections: **2026-05-21** → `ref-2026-05-21-1779436003269242000` (3 insights) — **…", state: "idle", msgs: 19, tokens: "11K", day: "Yesterday" },
    { id: "ec70b891-e5c5-4ca8-91d2-c7188013db72", short: "ec70b891", cli: "claude", title: "Both reflections recorded successfully. - **2026-05-20** → `ref-2026-05-20-1779382150875357000` (2…", state: "idle", msgs: 82, tokens: "29K", day: "May 21" },
    { id: "15492d08-0d59-40e8-9826-5b18cade4d3b", short: "15492d08", cli: "claude", title: "I can see the context — I made a mistake in my previous response by saying I'd skip the spec doc.", state: "idle", msgs: 12, tokens: "6K", day: "May 21" },
    { id: "1676524d-2a03-4963-9937-a9ede88f51c2", short: "1676524d", cli: "claude", title: "## Rolling Summary **Context:** User is working on a Example frontend customer management feature.", state: "idle", msgs: 4, tokens: "3K", day: "May 21" },
  ],

  // Worklog reflections for the selected project (klyne)
  worklog: {
    project: "klyne",
    state: "stale",
    newSince: 3,
    refreshCmd: 'cd "/Users/you/Desktop/Project/klyne" && claude -p --permission-mode bypassPermissions \'/klyne:reflect\'',
    reflections: [
      {
        date: "2026-05-22",
        evidence: "1 evidence · tier 1 · ai",
        bullets: [
          "Closed the MCP-registration gap that kept /klyne:reflect from resolving outside the klyne project — promoted klyne stdio server to user-scope mcpServers in ~/.claude.json.",
          "Replaced BuildReport's first-wins rep.ReflectionMarkdown rule with `gatherProjectBullets` in ui/src/routes/productivity/+page.svelte. Per-bullet repo chip, de-duped by title key.",
          "Shipped iterative cursor-based reflection feature in three commits after locking the design (A1+B+C1+D1) in docs/features/iterative-reflection.md.",
        ],
        shipped: ["`init` — pushed-to-remote, 7 commit(s), latest fc16a029 on 2026-05-22"],
      },
      {
        date: "2026-05-21",
        evidence: "3 evidence · tier 2 · ai",
        bullets: [
          "Drove the per-session token timeline accuracy fix end-to-end — uncached portion now shown as a thinner overlay on the prefix curve.",
          "Cleaned reflection-evidence rendering at the recorder layer with `dedupeOrdered` + `equalStringSet` helpers in internal/worklog/reflection_recorder.go.",
        ],
        shipped: [],
      },
    ],
  },

  // Insights: tokens-per-day, daily series, model breakdown
  insights: {
    spend: "$12,221.22",
    inputTokens: "4.99B",
    outputTokens: "38.1M",
    cachedPct: "95% cached",
    sessions: 4718,
    messages: "69K",
    perDay: [
      120, 80, 90, 110, 100, 95, 80, 70, 60, 80, 110, 140, 220, 320, 380, 480, 620, 980, 1500, 2200,
      1900, 1400, 1100, 900, 700, 500, 380, 260, 200, 180,
    ],
    perDayLabels: ["Apr 27", "May 23"],
    modelsByCost: [
      { name: "claude-opus-4-7", cost: 11744.29, pct: 96 },
      { name: "gpt-5.5", cost: 469.23, pct: 3.8 },
      { name: "claude-opus-4-6", cost: 7.70, pct: 0.1 },
      { name: "<synthetic>", cost: 0, pct: 0 },
      { name: "claude-haiku-4-5-20251001", cost: 0, pct: 0 },
    ],
    agentMix: { claude: 98, codex: 2, claudeTokens: "4.94B", codexTokens: "85M", claudeMsgs: "64K", codexMsgs: "4K" },
    projectsRanked: [
      { name: "klyne", tokens: "2.06B", trend: "0%", cacheHit: "95%", tokMsg: "75K", compact: "3503/3508", pct: 100 },
      { name: "Desktop", tokens: "701M", trend: "0%", cacheHit: "98%", tokMsg: "170K", compact: "17/15", pct: 34 },
      { name: "observer-sessions", tokens: "395M", trend: "0%", cacheHit: "88%", tokMsg: "34K", compact: "51/924", pct: 19 },
      { name: "ops-app", tokens: "284M", trend: "+12%", cacheHit: "91%", tokMsg: "47K", compact: "12/120", pct: 14 },
      { name: "consult-service", tokens: "94M", trend: "-4%", cacheHit: "94%", tokMsg: "22K", compact: "3/20", pct: 5 },
    ],
    // Heatmap: 7 rows (weekdays), 14 cols (weeks). 0-4 intensity.
    heatmap: Array.from({length: 7}, (_, r) =>
      Array.from({length: 14}, (_, c) => {
        const seed = (r * 31 + c * 17) % 100;
        if (seed < 30) return 0;
        if (seed < 55) return 1;
        if (seed < 75) return 2;
        if (seed < 90) return 3;
        return 4;
      })
    ),
    streak: 14,
    longestStreak: 28,
    favoriteModel: "claude-opus-4-7",
    peakHour: "14:00 – 16:00",
  },

  runbooks: {
    tags: ["bao", "dev", "infra", "labstack", "multi-repo", "openbao", "runbook", "secrets"],
    items: [
      { id: "d-97e7df766f", scope: "global", title: "OpenBao (bao) dev secrets runbook — DEV ONLY, not available on prod", ago: "3d", project: null, tags: ["runbook","secrets","openbao","bao","dev","infra"], body: "⚠ SCOPE: This applies to the dev environment only. Prod does NOT use this script / setup.\n\nBefore using, confirm the infra is configured for the current context and that you are pointed at dev.\n\nPre-req: check the infra is set up (OpenBao reachable, kubeconfig pointed at dev namespace, script present at ./scripts/openbao/bao-secrets.sh)." },
      { id: "d-bc9f23cbf1", scope: "project", project: "product-svc", title: "Labstack span spans 4 projects — multi-repo worktree note", ago: "3d", tags: ["labstack","multi-repo","runbook"], body: "Labstack work spans 4 projects, each with a labstack worktree. When working on labstack changes, check/update across all four: product-svc, ops-app, consult-service, customer-svc. Coordinate the rollout by lab tag." },
      { id: "d-201e2740b9", scope: "project", project: "ops-app", title: "Labstack worktree wiring for ops-app", ago: "3d", tags: ["labstack","multi-repo","runbook"], body: "Labstack work spans 4 projects. When working on labstack changes, check/update across all four. The worktree for ops-app lives at ../ops-app-labstack — push to its own branch first, never main." },
      { id: "d-6410c7d3c5", scope: "project", project: "consult-service", title: "Labstack worktree wiring for consult-service", ago: "3d", tags: ["labstack","multi-repo","runbook"], body: "Labstack work spans 4 projects. When working on labstack changes, check/update across all four. Pair with ops-app PRs by attaching the same lab tag." },
      { id: "d-9132ab44ef", scope: "project", project: "customer-svc", title: "Customer-svc labstack rollout — order of operations", ago: "5d", tags: ["labstack","runbook"], body: "Customer-svc is the last in the labstack rollout chain. Wait for product-svc, ops-app and consult-service to be deployed and smoke-tested before merging here." },
    ],
  },

  // Worklog reflections per project — own-page view (Worklog ≠ Projects).
  // Mix of states: stale (newer entries since last reflection), cold (none yet), fresh (covered).
  worklogProjects: [
    {
      project: "ops-app",
      path: "/Users/you/Desktop/Learning/ops-app",
      state: "stale", newSince: 2,
      reflectedAgo: "18h", lastActivityAgo: "55m",
      latest: {
        date: "2026-05-22",
        evidence: "1 evidence · tier 1 · ai",
        bullets: [
          "Day's highest-impact work landed on feature/TICKET-1455-extend-fallback-slot-window, with the fallback slot window extension past 8 PM in create booking (f1aee959, +341/-153) as the headline visit-booking fix.",
          "Working style was end-to-end fix-then-ship: the user repeatedly asked to 'fix this end to end', 'push this code', and explicitly requested separate tickets for follow-up issues rather than batching them into one branch.",
          "Visit-booking flexibility was the recurring theme alongside the fallback-slot fix: bookings were also allowed without member DOB or gender (b6e579fa), broadening the set of cases a booker can complete in one pass.",
          "Tickets were spun out per defect instead of piggybacking, indicating a preference for traceability — separate CLI tickets created and pushed in the same session as the underlying fix.",
        ],
        shipped: ["`feature/TICKET-1455-extend-fallback-slot-window` [TICKET-1455] — pushed-to-remote, 3 commit(s), latest f1aee959 on 2026-05-22"],
        openLoops: [
          "unpushed: `feature/TICKET-1455-extend-fallback-slot-window` — 35 commit(s) ahead, not on origin (just now)",
          "unpushed: `feature/TICKET-1455-extend-fallback-slot-window` — 4 commit(s) ahead, not on origin (just now)",
        ],
      },
      refreshCmd: 'cd "/Users/you/Desktop/Learning/ops-app" && claude -p --permission-mode bypassPermissions \'/klyne:reflect\'',
    },
    {
      project: "customer-svc",
      path: "/Users/you/Desktop/Learning/customer-svc",
      state: "cold", newSince: 2,
      reflectedAgo: null, lastActivityAgo: "17h",
      latest: null,
      refreshCmd: 'cd "/Users/you/Desktop/Learning/customer-svc" && claude -p --permission-mode bypassPermissions \'/klyne:reflect\'',
    },
    {
      project: "klyne",
      path: "/Users/you/Desktop/Project/klyne",
      state: "stale", newSince: 3,
      reflectedAgo: "17h", lastActivityAgo: "16h",
      latest: {
        date: "2026-05-22",
        evidence: "1 evidence · tier 1 · ai",
        bullets: [
          "Closed the MCP-registration gap that kept /klyne:reflect from resolving outside the klyne project — promoted the klyne stdio server to user-scope mcpServers in ~/.claude.json.",
          "Made the Productivity 'WHAT WAS DONE' hero truly multi-project: replaced BuildReport's first-wins rep.ReflectionMarkdown rule with `gatherProjectBullets`.",
          "Shipped the full iterative cursor-based reflection feature in three commits after locking the design (A1+B+C1+D1) in docs/features/iterative-reflection.md.",
        ],
        shipped: ["`init` — pushed-to-remote, 7 commit(s), latest fc16a029 on 2026-05-22"],
        openLoops: [],
      },
      refreshCmd: 'cd "/Users/you/Desktop/Project/klyne" && claude -p --permission-mode bypassPermissions \'/klyne:reflect\'',
    },
    {
      project: "consult-service",
      path: "/Users/you/code/consult-service",
      state: "fresh", newSince: 0,
      reflectedAgo: "20h", lastActivityAgo: "20h",
      latest: {
        date: "2026-05-21",
        evidence: "2 evidence · tier 1 · ai",
        bullets: [
          "Wired the new intake schema to /api/intake/v2 with full contract tests; three fields promoted to required (dob, primary_phone, consent_at).",
          "Dropped stale `triage_v1` import from IntakeForm.svelte after reading the component end-to-end — no functional change but reduces surface area.",
        ],
        shipped: [],
        openLoops: [],
      },
      refreshCmd: 'cd "/Users/you/code/consult-service" && claude -p --permission-mode bypassPermissions \'/klyne:reflect\'',
    },
    {
      project: "product-svc",
      path: "/Users/you/code/product-svc",
      state: "cold", newSince: 8,
      reflectedAgo: null, lastActivityAgo: "1d",
      latest: null,
      refreshCmd: 'cd "/Users/you/code/product-svc" && claude -p --permission-mode bypassPermissions \'/klyne:reflect\'',
    },
  ],


  searchSuggest: ["payment", "race condition", "refactor", "stripe", "redis", "dedupe", "rollback", "migration"],
};

window.FX = FX;
