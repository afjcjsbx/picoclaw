// @ts-check
// Docs site for PicoClaw. Markdown lives in ../docs (unchanged, readable on
// GitHub); this directory only holds the site shell.

/** @type {import('@docusaurus/types').Config} */
const config = {
  title: 'PicoClaw',
  tagline: 'Ultra-lightweight personal AI assistant in Go',
  favicon: 'img/favicon.ico',

  url: 'https://afjcjsbx.github.io',
  baseUrl: '/picoclaw/',
  organizationName: 'afjcjsbx',
  projectName: 'picoclaw',
  trailingSlash: true,

  onBrokenLinks: 'warn',
  onBrokenAnchors: 'warn',

  // Docs are plain CommonMark written for GitHub, not MDX: "{" and "<" in
  // prose must not be parsed as JSX.
  markdown: {
    format: 'detect',
    hooks: {onBrokenMarkdownLinks: 'warn'},
  },

  i18n: {
    defaultLocale: 'en',
    locales: ['en', 'zh-Hans'],
    localeConfigs: {
      en: {label: 'English', htmlLang: 'en-US'},
      'zh-Hans': {label: '简体中文', htmlLang: 'zh-Hans'},
    },
  },

  presets: [
    [
      'classic',
      /** @type {import('@docusaurus/preset-classic').Options} */
      ({
        docs: {
          path: '../docs',
          routeBasePath: 'docs',
          sidebarPath: './sidebars.js',
          editUrl: 'https://github.com/afjcjsbx/picoclaw/edit/main/docs/',
          // X.zh.md sits beside X.md in the repo; scripts/sync-i18n.mjs
          // copies it into the zh-Hans locale tree. The root README.md is
          // contributor guidance for GitHub, not site content.
          exclude: ['**/_*/**', '**/*.zh.md', 'README.md'],
          beforeDefaultRemarkPlugins: [require('./src/remark/github-compat')],
        },
        blog: false,
        theme: {customCss: './src/css/custom.css'},
        sitemap: {changefreq: 'weekly', priority: 0.5},
      }),
    ],
  ],

  plugins: [
    [
      // Old Jekyll URLs (/guides/providers.html, /guides/) keep working.
      '@docusaurus/plugin-client-redirects',
      {
        createRedirects(existingPath) {
          if (!existingPath.startsWith('/docs/')) return undefined;
          const old = existingPath.slice('/docs'.length).replace(/\/$/, '');
          return old ? [`${old}.html`] : undefined;
        },
      },
    ],
  ],

  themes: [
    [
      '@easyops-cn/docusaurus-search-local',
      /** @type {import('@easyops-cn/docusaurus-search-local').PluginOptions} */
      ({
        hashed: true,
        language: ['en', 'zh'],
        docsRouteBasePath: '/docs',
        docsDir: '../docs',
        indexBlog: false,
        highlightSearchTermsOnTargetPage: true,
        explicitSearchResultPath: true,
      }),
    ],
  ],

  themeConfig:
    /** @type {import('@docusaurus/preset-classic').ThemeConfig} */
    ({
      image: 'img/logo.png',
      colorMode: {respectPrefersColorScheme: true},
      docs: {sidebar: {hideable: true, autoCollapseCategories: true}},
      navbar: {
        title: 'PicoClaw',
        logo: {alt: 'PicoClaw', src: 'img/logo.png'},
        items: [
          {type: 'docSidebar', sidebarId: 'docs', label: 'Docs', position: 'left'},
          {type: 'localeDropdown', position: 'right'},
          {href: 'https://github.com/afjcjsbx/picoclaw/releases', label: 'Releases', position: 'right'},
          {href: 'https://github.com/afjcjsbx/picoclaw', label: 'GitHub', position: 'right'},
          {type: 'search', position: 'right'},
        ],
      },
      footer: {
        style: 'dark',
        links: [
          {
            title: 'Docs',
            items: [
              {label: 'Quick Start', to: '/docs/guides/docker'},
              {label: 'Providers', to: '/docs/guides/providers'},
              {label: 'Configuration', to: '/docs/guides/configuration'},
              {label: 'FAQ', to: '/docs/guides/faq'},
            ],
          },
          {
            title: 'Reference',
            items: [
              {label: 'CLI', to: '/docs/reference/cli'},
              {label: 'Tools', to: '/docs/tools'},
              {label: 'Gateway API', to: '/docs/reference/gateway-api'},
              {label: 'Security', to: '/docs/security'},
            ],
          },
          {
            title: 'Project',
            items: [
              {label: 'GitHub', href: 'https://github.com/afjcjsbx/picoclaw'},
              {label: 'Releases', href: 'https://github.com/afjcjsbx/picoclaw/releases'},
              {label: 'Issues', href: 'https://github.com/afjcjsbx/picoclaw/issues'},
              {label: 'Maintenance status', to: '/docs/project/maintenance-status'},
            ],
          },
        ],
        copyright: 'PicoClaw community fork · MIT License',
      },
      prism: {
        additionalLanguages: ['bash', 'powershell', 'json', 'go', 'yaml', 'toml', 'diff'],
      },
    }),
};

module.exports = config;
