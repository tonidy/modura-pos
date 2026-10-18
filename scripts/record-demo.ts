/**
 * Turns the real output of demo-schema-change.sh into docs/media/schema-change.gif.
 *
 * Every frame is rendered from what the script actually printed, so the GIF cannot
 * drift from the behavior it shows. Needs ffmpeg and Playwright's Chromium.
 *
 *   pnpm --filter @modura/scripts record-demo
 */
import { execFileSync, spawnSync } from 'node:child_process';
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { chromium } from '@playwright/test';

interface Block {
  command: string;
  output: string[];
}
interface Step {
  title: string;
  blocks: Block[];
}

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const target = join(root, 'docs/media/schema-change.gif');

const demo = spawnSync('bash', [join(root, 'scripts/demo-schema-change.sh')], {
  cwd: root,
  encoding: 'utf8',
  maxBuffer: 16 * 1024 * 1024,
});
if (demo.status !== 0) {
  process.stderr.write(demo.stdout + demo.stderr);
  throw new Error('demo-schema-change.sh failed; not recording a GIF of a broken demo');
}

const steps = parse(demo.stdout);
const frames = steps.flatMap((step, index) => [
  // The command alone first, as if just typed, then the result.
  { html: render(step, index, steps.length, false), seconds: 1.2 },
  { html: render(step, index, steps.length, true), seconds: holdFor(step) },
]);

const work = mkdtempSync(join(tmpdir(), 'modura-gif-'));
try {
  const browser = await chromium.launch();
  const page = await browser.newPage({ viewport: { width: 1120, height: 660 }, deviceScaleFactor: 1 });
  const list: string[] = [];
  for (const [i, frame] of frames.entries()) {
    const file = join(work, `frame-${String(i).padStart(3, '0')}.png`);
    await page.setContent(frame.html);
    await page.screenshot({ path: file });
    list.push(`file '${file}'`, `duration ${frame.seconds}`);
  }
  await browser.close();
  // The concat demuxer ignores the last duration unless the final file is repeated.
  list.push(list.at(-2)!);
  writeFileSync(join(work, 'frames.txt'), `${list.join('\n')}\n`);

  mkdirSync(dirname(target), { recursive: true });
  execFileSync('ffmpeg', [
    '-y', '-loglevel', 'error',
    '-f', 'concat', '-safe', '0', '-i', join(work, 'frames.txt'),
    '-vf', 'fps=4,split[a][b];[a]palettegen=max_colors=64[p];[b][p]paletteuse=dither=none',
    '-loop', '0',
    target,
  ]);
  const total = frames.reduce((sum, f) => sum + f.seconds, 0);
  console.log(`Wrote ${target} (${frames.length} frames, ${total.toFixed(1)}s)`);
} finally {
  rmSync(work, { recursive: true, force: true });
}

function parse(output: string): Step[] {
  const parsed: Step[] = [];
  for (const line of output.split('\n')) {
    if (line.startsWith('### ')) parsed.push({ title: line.slice(4), blocks: [] });
    else if (line.startsWith('$ ')) parsed.at(-1)?.blocks.push({ command: line.slice(2), output: [] });
    else if (line.trim() !== '' && !line.startsWith('Demo complete')) parsed.at(-1)?.blocks.at(-1)?.output.push(line);
  }
  return parsed;
}

function holdFor(step: Step): number {
  const lines = step.blocks.reduce((n, b) => n + 1 + b.output.length, 0);
  return Math.min(5, 2 + lines * 0.12);
}

function render(step: Step, index: number, count: number, withOutput: boolean): string {
  const body = step.blocks
    .map((block, i) => {
      // Show every block of the step, but only the first command before output appears.
      if (!withOutput && i > 0) return '';
      const output = withOutput ? block.output.map((l) => `<div class="${classify(l)}">${escape(l)}</div>`).join('') : '';
      return `<div class="cmd"><span class="prompt">$</span> ${escape(block.command)}</div>${output}`;
    })
    .join('');
  return `<!doctype html><html><head><style>
    body { margin: 0; background: #f4f1ec; font-family: system-ui, sans-serif; }
    .win { margin: 24px; height: 612px; border-radius: 12px; background: #1b1a17; box-shadow: 0 8px 30px rgb(0 0 0 / 25%); overflow: hidden; display: flex; flex-direction: column; }
    .bar { display: flex; align-items: center; gap: 8px; padding: 12px 16px; background: #26241f; color: #a39b8e; font-size: 13px; }
    .dot { width: 12px; height: 12px; border-radius: 50%; background: #4a463f; }
    .bar .label { margin-left: 8px; }
    .bar .count { margin-left: auto; font-variant-numeric: tabular-nums; }
    .title { padding: 18px 22px 6px; color: #f1ede6; font-size: 22px; font-weight: 650; }
    .term { padding: 8px 22px 20px; font: 14.5px/1.5 ui-monospace, 'DejaVu Sans Mono', Menlo, monospace; color: #d9d3c8; white-space: pre-wrap; overflow: hidden; }
    .cmd { margin-top: 10px; color: #f1ede6; }
    .prompt { color: #7fb8a5; }
    .add { color: #8fd18f; } .del { color: #f0897d; } .err { color: #f0897d; }
    .warn { color: #e7b25c; } .ok { color: #7fb8a5; } .meta { color: #7d766b; }
  </style></head><body><div class="win">
    <div class="bar"><span class="dot"></span><span class="dot"></span><span class="dot"></span>
      <span class="label">modura: one contract change, every surface</span><span class="count">${index + 1} / ${count}</span></div>
    <div class="title">${escape(step.title)}</div>
    <div class="term">${body}</div>
  </div></body></html>`;
}

function classify(line: string): string {
  if (/^(diff --git|index |@@|---|\+\+\+)/.test(line)) return 'meta';
  if (/error|unknown field|refuse|need a major/i.test(line)) return 'err';
  if (line.startsWith('+') || line.startsWith('  +')) return 'add';
  if (line.startsWith('-')) return 'del';
  if (line.startsWith('  !') || line.startsWith('Bump')) return 'warn';
  if (/^wrote |up to date|\|/.test(line)) return 'ok';
  return '';
}

function escape(text: string): string {
  return text.replaceAll('&', '&amp;').replaceAll('<', '&lt;').replaceAll('>', '&gt;');
}
