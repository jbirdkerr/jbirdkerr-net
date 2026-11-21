// Screenshot a slice of a workflow's Event History (full "History" view, newest first).
// Rows outside <fromEventId>..<toEventId> are hidden so the table header sits right above
// the slice; the listed event IDs are expanded to show their details.
//
// Usage: node events.mjs <workflowId> <out.png> <fromEventId> <toEventId> [expandIds]
//   expandIds: comma-separated event IDs to expand, e.g. "24" or "24,41"
import { CLIP_WIDTH, CLIP_X, openWorkflow, usage } from './lib.mjs';

const [workflowId, out, fromId, toId, expand = ''] = process.argv.slice(2);
if (!workflowId || !out || !fromId || !toId) {
  usage('Usage: node events.mjs <workflowId> <out.png> <fromEventId> <toEventId> [expandIds]');
}

const { browser, page } = await openWorkflow(workflowId, { height: 4000 });
try {
  const row = (eventId) => page.locator(`tr[data-testid=event-summary-row][id="${eventId}"]`);

  for (const eventId of expand.split(',').filter(Boolean)) {
    await row(eventId).click();
    await page.waitForTimeout(500);
  }
  await page.waitForTimeout(600);

  await page.evaluate(
    ([from, to]) => {
      for (const r of document.querySelectorAll('tr[data-testid=event-summary-row]')) {
        const id = Number(r.id);
        if (id > from || id < to) {
          r.style.display = 'none';
          const detail = r.nextElementSibling;
          if (detail && !detail.matches('[data-testid=event-summary-row]')) detail.style.display = 'none';
        }
      }
    },
    [Number(fromId), Number(toId)],
  );
  await page.waitForTimeout(300);

  const { top, bottom } = await page.evaluate(() => {
    const heading = [...document.querySelectorAll('h2')].find((h) => h.textContent.includes('Event History'));
    const table = [...document.querySelectorAll('table')].pop();
    return { top: heading.getBoundingClientRect().top, bottom: table.getBoundingClientRect().bottom };
  });

  await page.screenshot({
    path: out,
    clip: { x: CLIP_X, y: top - 16, width: CLIP_WIDTH, height: bottom - top + 28 },
  });
} finally {
  await browser.close();
}
