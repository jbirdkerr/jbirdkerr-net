// Shared helpers for capturing Temporal UI screenshots for the
// temporal-purchase-saga-demo post. See README.md in this directory.
import { chromium } from 'playwright';

export const TEMPORAL_UI = process.env.TEMPORAL_UI ?? 'http://localhost:8233';
export const NAMESPACE = process.env.TEMPORAL_NAMESPACE ?? 'default';

// Horizontal crop that drops the UI's left nav and right gutter at 1440px wide.
export const CLIP_X = 80;
export const CLIP_WIDTH = 1345;
// Top of the workflow header (status badge + workflow ID), below the namespace bar.
export const HEADER_TOP = 110;

export async function openWorkflow(workflowId, { height = 2400 } = {}) {
  const browser = await chromium.launch();
  const page = await browser.newPage({
    viewport: { width: 1440, height },
    deviceScaleFactor: 2,
  });
  const url = `${TEMPORAL_UI}/namespaces/${NAMESPACE}/workflows/${encodeURIComponent(workflowId)}`;
  await page.goto(url, { waitUntil: 'networkidle' });
  await page.waitForTimeout(800);
  return { browser, page };
}

export function usage(text) {
  console.error(text);
  process.exit(2);
}
