// Screenshot a workflow's header and Timeline card (Summary collapsed, timeline fit to width).
//
// Usage: node timeline.mjs <workflowId> <out.png>
import { CLIP_WIDTH, CLIP_X, HEADER_TOP, openWorkflow, usage } from './lib.mjs';

const [workflowId, out] = process.argv.slice(2);
if (!workflowId || !out) usage('Usage: node timeline.mjs <workflowId> <out.png>');

const { browser, page } = await openWorkflow(workflowId);
try {
  await page.getByRole('button', { name: 'Summary' }).click();
  await page.getByRole('button', { name: 'Fit' }).click();
  await page.waitForTimeout(1000);

  // Walk up from the "Timeline" heading to its card and take the card's bottom edge.
  const bottom = await page.evaluate(() => {
    const heading = [...document.querySelectorAll('h2')].find((h) => h.textContent.trim() === 'Timeline');
    let card = heading;
    while (card && card.getBoundingClientRect().height < 200) card = card.parentElement;
    return card.getBoundingClientRect().bottom;
  });

  await page.screenshot({
    path: out,
    clip: { x: CLIP_X, y: HEADER_TOP, width: CLIP_WIDTH, height: bottom + 12 - HEADER_TOP },
  });
} finally {
  await browser.close();
}
