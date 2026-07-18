// SPDX-License-Identifier: Apache-2.0
// M11 acceptance: Playwright drive of the read-only console against a
// seeded stack. Every network request the page makes is intercepted; any
// non-GET/HEAD request fails the run (proxy-asserted zero writes). The
// provenance requirement is asserted as UI: calibration age and per-metric
// methodology must be RENDERED, not merely present in API payloads.
//
// Usage: node hack/e2e-console.mjs <base-url> <token> <job-id>
import { chromium } from "playwright";

const [base, token, jobId] = process.argv.slice(2);
if (!base || !token || !jobId) {
  console.error("usage: node e2e-console.mjs <base-url> <token> <job-id>");
  process.exit(2);
}

const browser = await chromium.launch();
const page = await browser.newPage();
const violations = [];
const failures = [];

await page.route("**/*", (route) => {
  const req = route.request();
  const url = new URL(req.url());
  if (url.origin === new URL(base).origin && !["GET", "HEAD"].includes(req.method())) {
    violations.push(`${req.method()} ${req.url()}`);
  }
  route.continue();
});

const expect = async (desc, fn) => {
  try {
    await fn();
    console.log(`ok  ${desc}`);
  } catch (err) {
    failures.push(`${desc}: ${err}`);
    console.error(`FAIL ${desc}: ${err}`);
  }
};

await page.goto(`${base}/console/`);
await expect("token gate shows before auth", () =>
  page.waitForSelector("#token-gate:not([hidden])", { timeout: 5000 }));
await page.fill("#token-input", token);
await page.click("#token-save");

// Fleet view: calibration age + methodology are rendered (provenance as UI).
await expect("fleet renders target cards", () =>
  page.waitForSelector(".card.target", { timeout: 10000 }));
await expect("calibration age is rendered", async () => {
  const text = await page.textContent(".cal-age");
  if (!/ago|just now/.test(text)) throw new Error(`age text: ${text}`);
});
await expect("metric methodology is rendered", async () => {
  const cells = await page.$$eval("td.methodology", (els) => els.map((e) => e.textContent));
  if (!cells.length || !cells.some((c) => c && c !== "—")) {
    throw new Error(`methodology cells: ${JSON.stringify(cells)}`);
  }
});

// Jobs explorer.
await page.click('a[data-nav="jobs"]');
await expect("jobs table renders rows", () =>
  page.waitForSelector("table.jobs tbody tr", { timeout: 10000 }));

// Placement-audit explorer: the "why did my job land there" page.
await page.goto(`${base}/console/#job/${jobId}`);
await expect("placement audit explains the landing", () =>
  page.waitForSelector('h2:has-text("Why it landed there")', { timeout: 10000 }));
await expect("audit names the policy", async () => {
  const dl = await page.textContent("dl.audit");
  if (!/policy/.test(dl)) throw new Error(`audit facts: ${dl}`);
});
await expect("audit lists rejected targets with reasons", () =>
  page.waitForSelector("ul.rejected li", { timeout: 5000 }));

// Usage view.
await page.goto(`${base}/console/#usage`);
await page.fill("main input", "demo");
await page.press("main input", "Enter");
await expect("usage table renders native units", () =>
  page.waitForSelector('th:has-text("native units")', { timeout: 10000 }));

await browser.close();

if (violations.length) {
  console.error("WRITE CALLS ISSUED BY THE READ-ONLY CONSOLE:");
  for (const v of violations) console.error("  " + v);
  process.exit(1);
}
console.log("zero write calls (proxy-asserted)");
if (failures.length) process.exit(1);
console.log("CONSOLE-E2E OK");
