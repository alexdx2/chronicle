import test from 'node:test';
import assert from 'node:assert/strict';

// graph-renderer.js only touches d3/document/window inside methods, never
// at module scope (see marks-render.test.mjs's own note on this) — stub
// window defensively anyway, the way a real browser load would provide it.
globalThis.window = globalThis;
const { charBudget } = await import('../static/js/graph-renderer.js');

// Rev3 fix V1: card text (title, both subtitle lines, the node mark
// caption, the frame mark caption) used to truncate at counts tuned for
// one width/size — or, for the title, not truncate at all — so a subtitle
// like "provider · scoreboard-api" clipped at a flat 26 chars overflowed
// the 120px card it was drawn on (26 chars at 9px is ~140px). charBudget
// replaces every one of those fixed counts with a single pixel-aware
// rule: floor((widthPx - 10) / (0.6 * fontPx)).

test('charBudget: per-line budgets for a 120px node card, at each line’s own font size', () => {
  assert.equal(charBudget(120, 11), 16, 'title (11px)');
  assert.equal(charBudget(120, 9), 20, 'subtitle line 1 (9px)');
  assert.equal(charBudget(120, 8), 22, 'subtitle line 2 (8px)');
  assert.equal(charBudget(120, 8.5), 21, 'node mark caption (8.5px)');
});

test('charBudget: the old fixed subtitle count overflowed a 120px card; the new budget fits it', () => {
  // The bug report's own numbers: 26 chars at 9px is ~140px wide
  // (26 * 0.6 * 9 = 140.4), wider than the 120px card it was drawn on.
  const oldFixedCount = 26;
  assert.ok(oldFixedCount * 0.6 * 9 > 120, 'sanity: the old fixed count overflowed the card');

  const budget = charBudget(120, 9);
  const pxUsed = budget * 0.6 * 9 + 10; // + the same 10px padding charBudget reserves
  assert.ok(pxUsed <= 120, `budget of ${budget} chars (${pxUsed}px incl. padding) must fit the 120px card`);
});

test('charBudget: a wider group frame gets a larger budget than the fixed-width node card', () => {
  const nodeCardBudget = charBudget(120, 9);
  const wideFrameBudget = charBudget(240, 9);
  assert.ok(wideFrameBudget > nodeCardBudget);
  assert.equal(wideFrameBudget, 42);
});

test('charBudget: a smaller font fits more characters in the same width', () => {
  const at9px = charBudget(120, 9);
  const at8px = charBudget(120, 8);
  assert.ok(at8px > at9px);
});
