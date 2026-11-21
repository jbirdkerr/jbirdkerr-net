// Screenshot a Jaeger trace waterfall for a workflow, found by its Temporal workflow ID.
//
// Usage: node jaeger.mjs <workflowId> <out.png>
import { chromium } from 'playwright';
import { usage } from './lib.mjs';

const JAEGER = process.env.JAEGER_UI ?? 'http://localhost:16686';
const [workflowId, out] = process.argv.slice(2);
if (!workflowId || !out) usage('Usage: node jaeger.mjs <workflowId> <out.png>');

const tags = encodeURIComponent(JSON.stringify({ temporalWorkflowID: workflowId }));
const res = await fetch(`${JAEGER}/api/traces?service=order-worker&tags=${tags}&lookback=7d&limit=5`);
const traces = (await res.json()).data ?? [];
// Prefer the trace that starts at the API request, so the waterfall shows the full path.
const trace = traces.find((t) => t.spans.some((s) => s.operationName === 'POST /orders')) ?? traces[0];
if (!trace) usage(`No Jaeger trace found for ${workflowId}`);

const browser = await chromium.launch();
try {
  const page = await browser.newPage({ viewport: { width: 1080, height: 1200 }, deviceScaleFactor: 2 });
  await page.goto(`${JAEGER}/trace/${trace.traceID}?uiEmbed=v0`, { waitUntil: 'networkidle' });
  await page.waitForTimeout(1500);
  // Widen the "Service & Operation" column so span names aren't truncated.
  const dragger = page.locator('[class*="VerticalResizer--dragger"]').first();
  const d = await dragger.boundingBox();
  if (d) {
    await page.mouse.move(d.x + d.width / 2, d.y + 10);
    await page.mouse.down();
    await page.mouse.move(d.x + 110, d.y + 10, { steps: 8 });
    await page.mouse.up();
    await page.waitForTimeout(500);
  }
  const box = await page.evaluate(() => {
    const rows = [...document.querySelectorAll('.span-row')];
    const last = rows[rows.length - 1].getBoundingClientRect();
    return { bottom: last.bottom };
  });
  await page.screenshot({ path: out, clip: { x: 0, y: 0, width: 1080, height: box.bottom + 8 } });
  console.log(`${workflowId}: trace ${trace.traceID}`);
} finally {
  await browser.close();
}
