import puppeteer from '/Users/pen/.npm/_npx/ab5cd9f6d13a2312/node_modules/puppeteer/lib/esm/puppeteer/puppeteer.js';
import { pathToFileURL } from 'node:url';

const html = process.argv[2];
const out  = process.argv[3];

const browser = await puppeteer.launch({ headless: true, args: ['--font-render-hinting=none'] });
const page = await browser.newPage();
await page.goto(pathToFileURL(html).href, { waitUntil: 'networkidle0' });
await page.evaluateHandle('document.fonts.ready');

const foot = `
<div style="width:100%;font-family:'JetBrains Mono',Menlo,monospace;font-size:6.5pt;color:#8b8f96;
            padding:0 16mm;display:flex;justify-content:space-between;letter-spacing:0.08em;">
  <span style="text-transform:uppercase;">Orama Network &nbsp;·&nbsp; Whitepaper</span>
  <span>DeBros &nbsp;·&nbsp; Alpha</span>
  <span><span class="pageNumber"></span> / <span class="totalPages"></span></span>
</div>`;

await page.pdf({
  path: out,
  format: 'A4',
  printBackground: true,
  displayHeaderFooter: true,
  headerTemplate: '<div></div>',
  footerTemplate: foot,
  margin: { top: '18mm', bottom: '20mm', left: '16mm', right: '16mm' },
});

await browser.close();
console.log('wrote', out);
