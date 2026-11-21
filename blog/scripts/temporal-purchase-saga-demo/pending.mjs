// Screenshot a running workflow's Pending Activities tab.
//
// Usage: node pending.mjs <workflowId> <out.png>
import { CLIP_WIDTH, CLIP_X, HEADER_TOP, openWorkflow, TEMPORAL_UI, NAMESPACE, usage } from './lib.mjs';

const [workflowId, out] = process.argv.slice(2);
if (!workflowId || !out) usage('Usage: node pending.mjs <workflowId> <out.png>');

const { browser, page } = await openWorkflow(workflowId, { height: 1400 });
try {
  // openWorkflow lands on .../<runId>/history; swap to the pending-activities tab.
  const url = page.url().replace(/\/history$/, '/pending-activities');
  if (!url.startsWith(`${TEMPORAL_UI}/namespaces/${NAMESPACE}/`)) throw new Error(`Unexpected URL ${url}`);
  await page.goto(url, { waitUntil: 'networkidle' });
  await page.waitForTimeout(1200);

  const bottom = await page.evaluate(() => [...document.querySelectorAll('table')].pop().getBoundingClientRect().bottom);
  await page.screenshot({
    path: out,
    clip: { x: CLIP_X, y: HEADER_TOP, width: CLIP_WIDTH, height: bottom + 12 - HEADER_TOP },
  });
} finally {
  await browser.close();
}
