// Render workflow.mmd to a PNG in the blog's dark theme, using the locally installed
// Mermaid (no CDN) and the blog's self-hosted JetBrains Mono.
//
// Usage: node diagram.mjs [out.png]
import { readFile } from 'node:fs/promises';
import { createRequire } from 'node:module';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { chromium } from 'playwright';

const here = dirname(fileURLToPath(import.meta.url));
const require = createRequire(import.meta.url);
const out = process.argv[2] ?? join(here, '../../src/assets/blog/temporal-purchase-saga-demo/00-workflow.png');

const source = await readFile(join(here, 'workflow.mmd'), 'utf8');
const mermaidJs = require.resolve('mermaid/dist/mermaid.min.js');
const font = join(here, '../../node_modules/@fontsource/jetbrains-mono/files/jetbrains-mono-latin-400-normal.woff2');
const fontData = (await readFile(font)).toString('base64');

const BG = '#0a0e27'; // blog page background (src/styles/global.css)

const browser = await chromium.launch();
try {
  const page = await browser.newPage({ viewport: { width: 1200, height: 1600 }, deviceScaleFactor: 2 });
  await page.setContent(`<!doctype html><html><head><style>
    @font-face { font-family: 'JetBrains Mono'; src: url(data:font/woff2;base64,${fontData}) format('woff2'); }
    html, body { margin: 0; background: ${BG}; }
    #wrap { display: inline-block; padding: 24px; background: ${BG}; }
  </style></head><body><div id="wrap"><pre class="mermaid"></pre></div></body></html>`);
  await page.addScriptTag({ path: mermaidJs });
  await page.evaluate(async ({ source, BG }) => {
    document.querySelector('.mermaid').textContent = source;
    await document.fonts.load("16px 'JetBrains Mono'");
    window.mermaid.initialize({
      startOnLoad: false,
      theme: 'base',
      flowchart: { useMaxWidth: false, curve: 'basis', htmlLabels: true, padding: 14, nodeSpacing: 40, rankSpacing: 50 },
      themeCSS: `
        .edgeLabel, .edgeLabel p, .edgeLabel span { color: #cbd5e1 !important; background-color: ${BG} !important; }
        .flowchart-link { stroke-width: 1.75px !important; }
        .marker { fill: #94a3b8 !important; stroke: #94a3b8 !important; }
      `,
      themeVariables: {
        fontFamily: "'JetBrains Mono', monospace",
        fontSize: '16px',
        background: BG,
        lineColor: '#94a3b8',
        textColor: '#e2e8f0',
        edgeLabelBackground: BG,
      },
    });
    await window.mermaid.run({ querySelector: '.mermaid' });
  }, { source, BG });
  await page.waitForTimeout(300);
  await page.locator('#wrap').screenshot({ path: out });
  console.log(`wrote ${out}`);
} finally {
  await browser.close();
}
