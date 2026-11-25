import hljs from "highlight.js/lib/common"
import clojure from "highlight.js/lib/languages/clojure"
import cmake from "highlight.js/lib/languages/cmake"
import dart from "highlight.js/lib/languages/dart"
import dockerfile from "highlight.js/lib/languages/dockerfile"
import elixir from "highlight.js/lib/languages/elixir"
import erlang from "highlight.js/lib/languages/erlang"
import fsharp from "highlight.js/lib/languages/fsharp"
import groovy from "highlight.js/lib/languages/groovy"
import haskell from "highlight.js/lib/languages/haskell"
import julia from "highlight.js/lib/languages/julia"
import nginx from "highlight.js/lib/languages/nginx"
import ocaml from "highlight.js/lib/languages/ocaml"
import powershell from "highlight.js/lib/languages/powershell"
import properties from "highlight.js/lib/languages/properties"
import protobuf from "highlight.js/lib/languages/protobuf"
import scala from "highlight.js/lib/languages/scala"

hljs.registerLanguage("clojure", clojure)
hljs.registerLanguage("cmake", cmake)
hljs.registerLanguage("dart", dart)
hljs.registerLanguage("dockerfile", dockerfile)
hljs.registerLanguage("elixir", elixir)
hljs.registerLanguage("erlang", erlang)
hljs.registerLanguage("fsharp", fsharp)
hljs.registerLanguage("groovy", groovy)
hljs.registerLanguage("haskell", haskell)
hljs.registerLanguage("julia", julia)
hljs.registerLanguage("nginx", nginx)
hljs.registerLanguage("ocaml", ocaml)
hljs.registerLanguage("powershell", powershell)
hljs.registerLanguage("properties", properties)
hljs.registerLanguage("protobuf", protobuf)
hljs.registerLanguage("scala", scala)

// Highlighting very large files can block the main thread for long enough to
// make the preview panel feel frozen. Above this size the content is still
// shown as plain text.
export const MAX_HIGHLIGHTABLE_CHARS = 256 * 1024

const LANGUAGE_BY_FILENAME: Record<string, string> = {
  ".bash_profile": "bash",
  ".bashrc": "bash",
  ".dockerignore": "bash",
  ".editorconfig": "ini",
  ".env": "ini",
  ".gitattributes": "properties",
  ".gitignore": "bash",
  ".npmrc": "ini",
  ".profile": "bash",
  ".properties": "properties",
  ".yarnrc": "ini",
  ".zprofile": "bash",
  ".zshrc": "bash",
  brewfile: "ruby",
  "cmakelists.txt": "cmake",
  containerfile: "dockerfile",
  dockerfile: "dockerfile",
  gemfile: "ruby",
  "go.mod": "go",
  "go.sum": "go",
  "go.work": "go",
  jenkinsfile: "groovy",
  makefile: "makefile",
  "nginx.conf": "nginx",
  podfile: "ruby",
  procfile: "yaml",
  rakefile: "ruby",
  vagrantfile: "ruby",
}

const LANGUAGE_BY_EXTENSION: Record<string, string> = {
  adoc: "plaintext",
  astro: "xml",
  bash: "bash",
  c: "c",
  cc: "cpp",
  cfg: "ini",
  cjs: "javascript",
  clj: "clojure",
  cljc: "clojure",
  cljs: "clojure",
  cmake: "cmake",
  conf: "ini",
  cpp: "cpp",
  cs: "csharp",
  csh: "bash",
  css: "css",
  csx: "csharp",
  cts: "typescript",
  cxx: "cpp",
  dart: "dart",
  diff: "diff",
  dockerfile: "dockerfile",
  edn: "clojure",
  env: "ini",
  erl: "erlang",
  ex: "elixir",
  exs: "elixir",
  fish: "bash",
  fs: "fsharp",
  fsi: "fsharp",
  fsx: "fsharp",
  gemspec: "ruby",
  gql: "graphql",
  go: "go",
  gradle: "groovy",
  graphql: "graphql",
  groovy: "groovy",
  h: "cpp",
  hh: "cpp",
  hpp: "cpp",
  hrl: "erlang",
  hs: "haskell",
  htm: "xml",
  html: "xml",
  hxx: "cpp",
  ini: "ini",
  ipynb: "json",
  java: "java",
  jl: "julia",
  js: "javascript",
  json: "json",
  json5: "json",
  jsonc: "json",
  jsx: "javascript",
  ksh: "bash",
  kt: "kotlin",
  kts: "kotlin",
  less: "less",
  lua: "lua",
  m: "objectivec",
  mak: "makefile",
  markdown: "markdown",
  md: "markdown",
  mdx: "markdown",
  mjs: "javascript",
  mk: "makefile",
  mkd: "markdown",
  mkdn: "markdown",
  ml: "ocaml",
  mli: "ocaml",
  mm: "objectivec",
  mts: "typescript",
  nginx: "nginx",
  patch: "diff",
  php: "php",
  phtml: "php",
  pl: "perl",
  plist: "xml",
  pm: "perl",
  properties: "properties",
  proto: "protobuf",
  ps1: "powershell",
  psd1: "powershell",
  psm1: "powershell",
  py: "python",
  pyi: "python",
  pyw: "python",
  r: "r",
  rake: "ruby",
  rb: "ruby",
  rs: "rust",
  rss: "xml",
  sc: "scala",
  scala: "scala",
  scss: "scss",
  sh: "bash",
  sql: "sql",
  svelte: "xml",
  svg: "xml",
  swift: "swift",
  tsv: "plaintext",
  toml: "ini",
  ts: "typescript",
  tsx: "typescript",
  txt: "plaintext",
  vb: "vbnet",
  vbnet: "vbnet",
  vue: "xml",
  wat: "wasm",
  xhtml: "xml",
  xml: "xml",
  xsl: "xml",
  xslt: "xml",
  yaml: "yaml",
  yml: "yaml",
  zsh: "bash",
}

export function detectFileLanguage(path: string): string | null {
  const fileName = path.split(/[\\/]/).at(-1)?.toLocaleLowerCase() ?? ""
  if (!fileName) {
    return null
  }

  const exactMatch = LANGUAGE_BY_FILENAME[fileName]
  if (exactMatch) {
    return exactMatch
  }

  if (
    fileName.startsWith("dockerfile.") ||
    fileName.startsWith("containerfile.")
  ) {
    return "dockerfile"
  }

  if (fileName.startsWith(".env.")) {
    return "ini"
  }

  const extensionIndex = fileName.lastIndexOf(".")
  if (extensionIndex <= 0 || extensionIndex === fileName.length - 1) {
    return null
  }

  return LANGUAGE_BY_EXTENSION[fileName.slice(extensionIndex + 1)] ?? null
}

export function highlightCode(
  code: string,
  language: string | null,
): string | null {
  if (!language || code.length > MAX_HIGHLIGHTABLE_CHARS) {
    return null
  }

  try {
    return hljs.highlight(code, { language, ignoreIllegals: true }).value
  } catch {
    return null
  }
}
