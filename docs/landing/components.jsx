/* global React */
const { useState, useEffect, useRef } = React;

// Hero animated terminal
function HeroTerminal() {
  const lines = [
    { type: 'cmd', text: 'brew install traceary' },
    { type: 'out', text: '==> Installing traceary from duck8823/traceary\n==> Pouring traceary--0.14.0.arm64_sequoia.bottle.tar.gz\n🍺  /opt/homebrew/Cellar/traceary/0.14.0: 12 files, 18.4MB' },
    { type: 'cmd', text: 'traceary top --snapshot' },
    { type: 'out', html: true, text:
      '<span class="sess">4a70c526</span> workspace=<span class="ws">github.com/duck8823/traceary</span> agent=codex client=claude started=<span class="ts">07:06:37</span> latest=<span class="ts">07:06:58</span> events=165 last=<span class="kind">session_ended</span>: duration=29m21s\n' +
      '└── <span class="sess">7c91a2bf</span> workspace=<span class="ws">github.com/duck8823/traceary</span> agent=worker client=claude started=<span class="ts">07:03:12</span> latest=<span class="ts">07:06:52</span> events=42 last=<span class="kind">command_executed</span>: go test ./presentation/cli'
    },
    { type: 'cmd', text: 'traceary session handoff --compact-only' },
    { type: 'out', text: 'Investigating failing tests in application/usecase. Reproduced panic via `go test ./...`. Next: triage stacktrace.' },
  ];

  const [step, setStep] = useState(0);
  const [typed, setTyped] = useState('');
  const [phase, setPhase] = useState('typing'); // typing | output | done

  useEffect(() => {
    if (step >= lines.length) {
      const t = setTimeout(() => { setStep(0); setTyped(''); setPhase('typing'); }, 3500);
      return () => clearTimeout(t);
    }
    const line = lines[step];
    if (line.type === 'cmd') {
      if (phase === 'typing') {
        if (typed.length < line.text.length) {
          const t = setTimeout(() => setTyped(line.text.slice(0, typed.length + 1)), 32 + Math.random() * 30);
          return () => clearTimeout(t);
        } else {
          const t = setTimeout(() => setPhase('output'), 350);
          return () => clearTimeout(t);
        }
      } else {
        const t = setTimeout(() => { setStep(step + 1); setTyped(''); setPhase('typing'); }, 100);
        return () => clearTimeout(t);
      }
    } else {
      const t = setTimeout(() => { setStep(step + 1); setTyped(''); setPhase('typing'); }, 900);
      return () => clearTimeout(t);
    }
  }, [step, typed, phase]);

  // Render history
  const history = [];
  for (let i = 0; i < step; i++) {
    const l = lines[i];
    if (l.type === 'cmd') {
      history.push(<div className="term-line" key={`c${i}`}><span className="term-prompt">$</span><span className="term-cmd">{l.text}</span></div>);
    } else {
      if (l.html) {
        history.push(<div className="term-out" key={`o${i}`} dangerouslySetInnerHTML={{__html: l.text}} />);
      } else {
        history.push(<div className="term-out" key={`o${i}`}>{l.text}</div>);
      }
    }
  }

  // current line
  const current = lines[step];
  let cur = null;
  if (current && current.type === 'cmd') {
    cur = (
      <div className="term-line">
        <span className="term-prompt">$</span>
        <span className="term-cmd">{typed}<span className="cursor"/></span>
      </div>
    );
  }

  return (
    <div className="hero-terminal">
      <div className="term-bar">
        <span className="term-dot" style={{background:'#ef4444aa'}}/>
        <span className="term-dot" style={{background:'#f59e0baa'}}/>
        <span className="term-dot" style={{background:'#22c55eaa'}}/>
        <span className="term-title">~/src/traceary — zsh</span>
      </div>
      <div className="term-body">
        {history}
        {cur}
      </div>
    </div>
  );
}

// Inspect previews
const inspectViews = {
  top: {
    cmd: 'traceary top --snapshot',
    title: 'active session tree',
    body: (
      <>
        <div className="term-line"><span className="term-prompt">$</span><span className="term-cmd">traceary top --snapshot</span></div>
        <div className="term-out" dangerouslySetInnerHTML={{__html:
          '<span class="sess">4a70c526</span> workspace=<span class="ws">github.com/duck8823/traceary</span> agent=codex client=claude started=<span class="ts">07:06:37</span> latest=<span class="ts">07:06:58</span> events=165 last=<span class="kind">session_ended</span>: duration=29m21s\n' +
          '└── <span class="sess">7c91a2bf</span> workspace=<span class="ws">github.com/duck8823/traceary</span> agent=worker client=claude started=<span class="ts">07:03:12</span> latest=<span class="ts">07:06:52</span> events=42 last=<span class="kind">command_executed</span>: go test ./presentation/cli'
        }}/>
      </>
    )
  },
  tail: {
    cmd: 'traceary tail --limit 3',
    title: 'live event stream',
    body: (
      <>
        <div className="term-line"><span className="term-prompt">$</span><span className="term-cmd">traceary tail --limit 3</span></div>
        <div className="term-out" dangerouslySetInnerHTML={{__html:
          '<span class="ts">07:06:44</span>  <span class="kind">command_executed</span>  agent=codex   <span class="sess">sess=4a70c5</span>  <span class="ws">ws=traceary</span>  ls ~/.traceary 2>&1; find ~ -name "trace…\n' +
          '<span class="ts">07:06:47</span>  <span class="kind">command_executed</span>  agent=codex   <span class="sess">sess=4a70c5</span>  <span class="ws">ws=traceary</span>  ./traceary timeline --db-path /Users/duck8…\n' +
          '<span class="ts">07:06:52</span>  <span class="kind">command_executed</span>  agent=codex   <span class="sess">sess=4a70c5</span>  <span class="ws">ws=traceary</span>  timeout 1 ./traceary tail --db-path /Users/…'
        }}/>
      </>
    )
  },
  timeline: {
    cmd: 'traceary timeline --limit 2',
    title: 'gap-separated work blocks',
    body: (
      <>
        <div className="term-line"><span className="term-prompt">$</span><span className="term-cmd">traceary timeline --limit 2</span></div>
        <div className="term-out" dangerouslySetInnerHTML={{__html:
          '<span class="ts">2026-04-15 06:37 - 07:06</span> (29m21s) total events: 165\n' +
          '  <span class="ws">github.com/duck8823/traceary</span> (153) — 自律的に進めてください。\n' +
          '  <span class="ws">github.com/duck8823/dotfiles</span>  ( 12) — rust インストールしました\n' +
          '<span class="ts">2026-04-15 05:39 - 06:10</span> (31m1s) total events: 136\n' +
          '  <span class="ws">github.com/duck8823/traceary</span> (136) — &lt;analysis&gt; This conversation is a resumption…'
        }}/>
      </>
    )
  },
  search: {
    cmd: 'traceary search "panic: boom" --json',
    title: 'pinpoint events',
    body: (
      <>
        <div className="term-line"><span className="term-prompt">$</span><span className="term-cmd">traceary search "panic: boom" --json</span></div>
        <div className="term-out" dangerouslySetInnerHTML={{__html:
          '[\n  {\n    <span class="kind">"event_id"</span>: <span class="sess">"01J9QZ3W…"</span>,\n    <span class="kind">"kind"</span>: <span class="ws">"command_executed"</span>,\n    <span class="kind">"client"</span>: <span class="ws">"claude"</span>,\n    <span class="kind">"agent"</span>: <span class="ws">"codex"</span>,\n    <span class="kind">"session_id"</span>: <span class="sess">"4a70c526"</span>,\n    <span class="kind">"workspace"</span>: <span class="ws">"github.com/duck8823/traceary"</span>,\n    <span class="kind">"message"</span>: <span class="ws">"go test ./... → panic: boom"</span>,\n    <span class="kind">"created_at"</span>: <span class="ts">"2026-04-15T07:06:44Z"</span>\n  }\n]'
        }}/>
      </>
    )
  },
  handoff: {
    cmd: 'traceary session handoff',
    title: 'assembled working memory',
    body: (
      <>
        <div className="term-line"><span className="term-prompt">$</span><span className="term-cmd">traceary session handoff --workspace github.com/duck8823/traceary</span></div>
        <div className="term-out" dangerouslySetInnerHTML={{__html:
          '<span class="kind">TRACEARY HANDOFF</span>\n' +
          '<span class="kind">SESSION_ID</span>: <span class="sess">4a70c526</span>\n' +
          '<span class="kind">WORKSPACE</span>: <span class="ws">github.com/duck8823/traceary</span>\n' +
          '<span class="kind">LABEL</span>: triage panic\n' +
          '<span class="kind">STATUS</span>: in_progress\n' +
          '<span class="kind">TOTAL_EVENTS</span>: 165\n' +
          '<span class="kind">COMMAND_COUNT</span>: 12\n' +
          '<span class="kind">AGENTS</span>: codex, claude\n' +
          '<span class="kind">WORKING_STATE</span>:\n' +
          '- session_summary: Investigating failing tests in application/usecase. Reproduced panic via `go test ./...`.\n' +
          '- compact_summary: Next: triage stacktrace and isolate the regression.\n' +
          '<span class="kind">RECENT_COMMANDS</span>:\n' +
          '- go test ./...\n' +
          '- ./traceary tail --db-path /Users/...\n' +
          '- git status\n' +
          '<span class="kind">MEMORIES</span>:\n' +
          '- [decision][workspace:github.com/duck8823/traceary] Use `traceary session handoff --compact-only` for resume context.'
        }}/>
      </>
    )
  }
};

function InspectSection() {
  const [active, setActive] = useState('top');
  const items = [
    { id: 'top', cmd: 'traceary top', desc: 'Watch active root and subagent sessions with workspace and latest activity' },
    { id: 'tail', cmd: 'traceary tail', desc: 'Confirm hooks are firing, watch failures in real time' },
    { id: 'timeline', cmd: 'traceary timeline', desc: 'See gap-separated work blocks with per-workspace summaries' },
    { id: 'search', cmd: 'traceary search', desc: 'Jump to an exact kind, session, or query' },
    { id: 'handoff', cmd: 'traceary session handoff', desc: 'Resume with curated working memory' },
  ];
  const view = inspectViews[active];
  return (
    <div className="inspect-grid">
      <div className="inspect-list">
        {items.map(it => (
          <button
            key={it.id}
            className={`inspect-item ${active === it.id ? 'active' : ''}`}
            onClick={() => setActive(it.id)}
          >
            <span className="cmd">{it.cmd}</span>
            <span className="desc">{it.desc}</span>
          </button>
        ))}
      </div>
      <div className="inspect-preview">
        <div className="term-bar">
          <span className="term-dot" style={{background:'#ef4444aa'}}/>
          <span className="term-dot" style={{background:'#f59e0baa'}}/>
          <span className="term-dot" style={{background:'#22c55eaa'}}/>
          <span className="term-title">{view.title}</span>
        </div>
        <div className="term-body">
          {view.body}
        </div>
      </div>
    </div>
  );
}

// Install tabs
function InstallTabs() {
  const [tab, setTab] = useState('claude');
  const tabs = [
    { id: 'claude', label: 'Claude Code' },
    { id: 'codex', label: 'Codex' },
    { id: 'gemini', label: 'Gemini CLI' },
  ];
  const blocks = {
    claude: (
      <pre className="code-block">
<span className="cmt"># inside Claude Code</span>{'\n'}
<span className="kw">/plugin</span> marketplace add duck8823/traceary{'\n'}
<span className="kw">/plugin</span> install traceary
      </pre>
    ),
    codex: (
      <pre className="code-block">
<span className="cmt"># clone and launch Codex</span>{'\n'}
git clone https://github.com/duck8823/traceary <span className="var">~/src/traceary</span>{'\n'}
cd <span className="var">~/src/traceary</span> && codex{'\n'}
<span className="cmt"># inside Codex: /plugins → Traceary Plugins → Traceary</span>
      </pre>
    ),
    gemini: (
      <pre className="code-block">
<span className="cmt"># one-line installer</span>{'\n'}
bash <span className="kw">&lt;</span>(curl -sL https://raw.githubusercontent.com/duck8823/traceary/main/scripts/install-gemini-extension.sh)
      </pre>
    )
  };
  return (
    <div className="step-body">
      <div className="tabs" style={{marginBottom:14, gap:0}}>
        {tabs.map(t => (
          <button key={t.id} className={`tab ${tab===t.id?'active':''}`} onClick={() => setTab(t.id)} style={{padding:'8px 14px'}}>
            {t.label}
          </button>
        ))}
      </div>
      {blocks[tab]}
    </div>
  );
}

Object.assign(window, { HeroTerminal, InspectSection, InstallTabs });
