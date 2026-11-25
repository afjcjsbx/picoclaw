import React from 'react';
import Layout from '@theme/Layout';
import Link from '@docusaurus/Link';
import useBaseUrl from '@docusaurus/useBaseUrl';
import CodeBlock from '@theme/CodeBlock';

const INSTALL = `curl -fsSL https://raw.githubusercontent.com/afjcjsbx/picoclaw/main/scripts/install.sh | sh`;
const INSTALL_WIN = `irm https://raw.githubusercontent.com/afjcjsbx/picoclaw/main/scripts/install.ps1 | iex`;

const STATS = [
  ['10–20 MB', 'RAM at idle'],
  ['< 1 s', 'boot time'],
  ['1 binary', 'no runtime deps'],
  ['5 ISAs', 'RISC-V · ARM · MIPS · LoongArch · x86'],
];

const CARDS = [
  ['🚀', 'Quick Start', 'Install and run with the launcher or Docker.', '/docs/guides/docker'],
  ['🖥️', 'Web UI', 'Configure and chat from the browser.', '/docs/guides/webui'],
  ['🔌', 'Providers', 'model_list, fallbacks, per-model parameters.', '/docs/guides/providers'],
  ['💬', 'Chat Apps', 'Telegram, Discord, Slack, Matrix, WeCom and more.', '/docs/guides/chat-apps'],
  ['⚙️', 'Configuration', 'Workspace layout, env vars, sandbox settings.', '/docs/guides/configuration'],
  ['🛠️', 'Tools', 'Filesystem, exec, web, browser, cron, subagents.', '/docs/tools'],
  ['🧩', 'Plugins & Skills', 'Package skills and MCP servers as plugins.', '/docs/guides/plugins'],
  ['🔗', 'MCP', 'Connect any Model Context Protocol server.', '/docs/tools/mcp'],
  ['🧠', 'Memory & Sessions', 'Session scopes, Seahorse memory, pruning.', '/docs/guides/session-guide'],
  ['🔒', 'Security', 'Isolation, credential encryption, data filtering.', '/docs/security'],
  ['📟', 'CLI Reference', 'Every picoclaw command and flag.', '/docs/reference/cli'],
  ['🔬', 'Architecture', 'Agent loop, routing, hooks, runtime events.', '/docs/architecture'],
];

export default function Home() {
  return (
    <Layout
      title="Documentation"
      description="PicoClaw is an ultra-lightweight personal AI assistant in Go for RISC-V, ARM and other single-board computers. Setup, providers, chat apps, tools, MCP, plugins and architecture.">
      <header className="hero hero--home">
        <div className="container">
          <img className="hero__logo" src={useBaseUrl('/img/logo.png')} alt="" width="96" height="96" />
          <h1 className="hero__title">PicoClaw</h1>
          <p className="hero__subtitle">The featherweight AI agent that fits anywhere.</p>
          <div className="hero__buttons">
            <Link className="button button--primary button--lg" to="/docs/guides/docker">
              Get started
            </Link>
            <Link className="button button--secondary button--lg" to="/docs/guides/providers">
              Add a provider
            </Link>
            <Link className="button button--secondary button--lg" to="/docs/guides/chat-apps">
              Connect a chat app
            </Link>
            <Link className="button button--outline button--secondary button--lg" href="https://github.com/afjcjsbx/picoclaw">
              GitHub
            </Link>
          </div>
          <div className="hero__install">
            <CodeBlock language="bash" title="Linux & macOS">{INSTALL}</CodeBlock>
            <CodeBlock language="powershell" title="Windows">{INSTALL_WIN}</CodeBlock>
          </div>
        </div>
      </header>

      <main className="container margin-vert--lg">
        <div className="stats">
          {STATS.map(([big, small]) => (
            <div key={big} className="stats__item">
              <strong>{big}</strong>
              <span>{small}</span>
            </div>
          ))}
        </div>

        <h2 className="section__title">Where to next?</h2>
        <div className="cards">
          {CARDS.map(([icon, title, text, to]) => (
            <Link key={title} to={to} className="card-link">
              <span className="card-link__icon" aria-hidden="true">{icon}</span>
              <span className="card-link__title">{title}</span>
              <span className="card-link__text">{text}</span>
            </Link>
          ))}
        </div>

        <p className="margin-top--lg text--center">
          Looking for something specific? Press <kbd>Ctrl</kbd>/<kbd>⌘</kbd> + <kbd>K</kbd> to search, or read the{' '}
          <Link to="/docs/guides/faq">FAQ</Link> and{' '}
          <Link to="/docs/operations/troubleshooting">Troubleshooting</Link>.
        </p>
      </main>
    </Layout>
  );
}
