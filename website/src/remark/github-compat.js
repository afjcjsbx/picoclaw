// The docs are written for GitHub and the old Jekyll site; some GitHub-isms do
// not survive Docusaurus as-is:
//  - "> [!NOTE]" alert blockquotes become Infima alerts;
//  - directory links ("../channels/telegram/", "hooks/") must name the index
//    file so the markdown link resolver can map them to a page;
//  - sibling translation links ("X.zh.md"): inside the zh-Hans tree (see
//    scripts/sync-i18n.mjs) the translation is X.md; in the default locale they
//    point at the zh-Hans site. A translation with no English counterpart is
//    not published on the site, so it links to GitHub.
const fs = require('fs');
const path = require('path');

const SITE = 'https://afjcjsbx.github.io/picoclaw/zh-Hans/docs';
const GITHUB = 'https://github.com/afjcjsbx/picoclaw/blob/main/docs';
const DOCS = path.resolve(__dirname, '../../../docs');
const ZH_TREE = path.resolve(__dirname, '../../i18n/zh-Hans/docusaurus-plugin-content-docs/current');
const INDEXES = ['index.md', 'README.md'];
const ALERTS = {
  NOTE: ['info', 'Note'],
  TIP: ['success', 'Tip'],
  IMPORTANT: ['info', 'Important'],
  WARNING: ['warning', 'Warning'],
  CAUTION: ['danger', 'Caution'],
};

module.exports = function links() {
  return (tree, file) => {
    const fileDir = path.dirname(file.path);
    const inZhTree = !path.relative(ZH_TREE, fileDir).startsWith('..');
    const rel = path.relative(inZhTree ? ZH_TREE : DOCS, fileDir).split(path.sep).join('/');
    const walk = (node) => {
      const text = node.type === 'blockquote' && node.children[0]?.children?.[0];
      const alert = text?.type === 'text' && /^\[!([A-Z]+)\]\s*/.exec(text.value);
      if (alert && ALERTS[alert[1]]) {
        const [kind, title] = ALERTS[alert[1]];
        text.value = text.value.slice(alert[0].length);
        node.data = {hName: 'div', hProperties: {className: ['alert', `alert--${kind}`, 'margin-bottom--md'], role: 'note'}};
        node.children.unshift({type: 'paragraph', children: [{type: 'strong', children: [{type: 'text', value: title}]}]});
      }
      if (node.type === 'link' && node.url && !/^([a-z][a-z0-9+.-]*:|[/#])/i.test(node.url)) {
        const [target, hash = ''] = node.url.split(/(?=#)/);
        const abs = path.join(fileDir, target);
        if (/\.zh\.md$/.test(target)) {
          const docPath = path.posix.join(rel, target).replace(/\.zh\.md$/, '.md');
          if (!fs.existsSync(path.join(DOCS, docPath))) {
            node.url = `${GITHUB}/${docPath.replace(/\.md$/, '.zh.md')}${hash}`;
          } else if (inZhTree) {
            node.url = target.replace(/\.zh\.md$/, '.md') + hash;
          } else {
            node.url = `${SITE}/${docPath.replace(/\.md$/, '/').replace(/README\/$/, '')}${hash}`;
          }
        } else if (target && fs.existsSync(abs) && fs.statSync(abs).isDirectory()) {
          const index = INDEXES.find((i) => fs.existsSync(path.join(abs, i)));
          if (index) node.url = `${target.replace(/\/$/, '')}/${index}${hash}`;
        }
      }
      (node.children || []).forEach(walk);
    };
    walk(tree);
  };
};
