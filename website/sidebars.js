// @ts-check
// One sidebar, Hermes-style: collapsible categories, short labels (the H1s in
// docs/ carry emoji and long titles meant for GitHub readers).

const d = (id, label) => ({type: 'doc', id, label});
const cat = (label, indexId, items) => ({
  type: 'category',
  label,
  collapsed: true,
  link: indexId ? {type: 'doc', id: indexId} : undefined,
  items,
});

/** @type {import('@docusaurus/plugin-content-docs').SidebarsConfig} */
const sidebars = {
  docs: [
    d('index', 'Overview'),
    {
      type: 'category',
      label: 'Getting Started',
      collapsed: false,
      items: [
        d('guides/docker', 'Install & Quick Start'),
        d('guides/webui', 'Web UI & Launcher'),
        d('guides/providers', 'Providers & Models'),
        d('guides/chat-apps', 'Chat Apps'),
        d('guides/configuration', 'Configuration'),
        d('guides/hardware-compatibility', 'Hardware Compatibility'),
        d('guides/android-termux', 'Android (Termux)'),
        d('guides/faq', 'FAQ'),
      ],
    },
    cat('Guides', 'guides/README', [
      d('guides/session-guide', 'Sessions'),
      d('guides/routing-guide', 'Routing'),
      d('guides/spawn-tasks', 'Spawn & Async Tasks'),
      d('guides/plugins', 'Installing Plugins'),
      d('guides/writing-a-plugin', 'Writing a Plugin'),
      d('guides/skills-authoring', 'Authoring Skills'),
      d('guides/custom-commands', 'Custom Commands'),
      d('guides/custom-builds', 'Custom Builds'),
      d('guides/ANTIGRAVITY_USAGE', 'Antigravity Provider'),
    ]),
    {
      type: 'category',
      label: 'Channels',
      collapsed: true,
      items: [
        d('channels/telegram/README', 'Telegram'),
        d('channels/discord/README', 'Discord'),
        d('channels/slack/README', 'Slack'),
        d('channels/matrix/README', 'Matrix'),
        d('channels/deltachat/README', 'Delta Chat'),
        d('channels/line/README', 'LINE'),
        d('channels/vk/README', 'VK'),
        d('channels/mqtt/README', 'MQTT'),
        d('channels/a2a/README', 'A2A (Agent2Agent)'),
        d('channels/feishu/README', 'Feishu'),
        d('channels/dingtalk/README', 'DingTalk'),
        d('channels/wecom/README', 'WeCom'),
        d('channels/weixin/README', 'Weixin (WeChat)'),
        d('channels/qq/README', 'QQ'),
        d('channels/onebot/README', 'OneBot'),
        d('channels/maixcam/README', 'MaixCam'),
      ],
    },
    cat('Tools', 'tools/README', [
      d('tools/filesystem', 'Filesystem'),
      d('tools/exec', 'Exec'),
      d('tools/web', 'Web'),
      d('tools/browser-automation', 'Browser Automation'),
      d('tools/mcp', 'MCP'),
      d('tools/skills', 'Skills'),
      d('tools/subagents', 'Subagents'),
      d('tools/cron', 'Cron'),
      d('tools/todo', 'Todo'),
      d('tools/messaging', 'Messaging'),
      d('tools/media', 'Media'),
      d('tools/image-generation', 'Image Generation'),
      d('tools/hardware', 'Hardware'),
    ]),
    cat('Reference', 'reference/README', [
      d('reference/cli', 'CLI'),
      d('reference/tools_configuration', 'Tools Configuration'),
      d('reference/gateway-api', 'Gateway HTTP API'),
      d('reference/mcp-cli', 'MCP CLI'),
      d('reference/plugins-cli', 'Plugin CLI'),
      d('reference/cron', 'Cron & Scheduled Tasks'),
      d('reference/rate-limiting', 'Rate Limiting'),
      d('reference/config-versioning', 'Config Versioning'),
    ]),
    cat('Security', 'security/README', [
      d('security/security_configuration', 'Security Configuration'),
      d('security/isolation', 'Subprocess Isolation'),
      d('security/credential_encryption', 'Credential Encryption'),
      d('security/sensitive_data_filtering', 'Sensitive Data Filtering'),
      d('security/ANTIGRAVITY_AUTH', 'Antigravity Auth'),
    ]),
    cat('Operations', 'operations/README', [
      d('operations/troubleshooting', 'Troubleshooting'),
      d('operations/debug', 'Debugging'),
    ]),
    cat('Architecture', 'architecture/README', [
      d('architecture/session-system', 'Session System'),
      d('architecture/session-startup-and-pruning', 'Startup & Pruning'),
      d('architecture/seahorse-memory', 'Seahorse Memory'),
      d('architecture/routing-system', 'Routing System'),
      d('architecture/runtime-events', 'Runtime Events'),
      d('architecture/steering', 'Steering'),
      d('architecture/subturn', 'SubTurn'),
      d('architecture/parallel-tool-calls', 'Parallel Tool Calls'),
      d('architecture/agent-self-evolution', 'Agent Self-Evolution'),
      cat('Hooks', 'architecture/hooks/README', [
        d('architecture/hooks/hook-json-protocol', 'JSON-RPC Protocol'),
        d('architecture/hooks/plugin-tool-injection', 'Plugin Tool Injection'),
      ]),
      cat('Agent Refactor', 'architecture/agent-refactor/README', [
        d('architecture/agent-refactor/context', 'Context'),
        d('architecture/agent-refactor/loop-split', 'AgentLoop Split'),
        d('architecture/agent-refactor/agent-rename-plan', 'File Rename Plan'),
        d('architecture/agent-refactor/pipeline-restructuring-plan', 'Pipeline Restructuring'),
      ]),
    ]),
    cat('Design Notes', 'design/README', [
      d('design/steering-spec', 'Steering Spec'),
      d('design/parallel-tool-calls-spec', 'Parallel Tool Calls Spec'),
      d('design/provider-refactoring', 'Provider Refactoring'),
      d('design/provider-refactoring-tests', 'Provider Refactoring Tests'),
    ]),
    cat('Migration', 'migration/README', [
      d('migration/from-sipeed-picoclaw', 'From sipeed/picoclaw'),
      d('migration/model-list-migration', 'providers → model_list'),
    ]),
    d('project/maintenance-status', 'Maintenance Status'),
  ],
};

module.exports = sidebars;
