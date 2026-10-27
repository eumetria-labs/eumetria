import { test } from 'node:test';
import assert from 'node:assert/strict';
import { inline, parseReview } from '../site/js/reviewtext.js';
import { reviewNotices } from '../site/js/format.js';

test('inline bold and HTML escapes', () => {
  assert.deepEqual(inline('averaged **1,815 kcal** a day'), [{ text: 'averaged ' }, { text: '1,815 kcal', bold: true }, { text: ' a day' }]);
  assert.deepEqual(inline('Sleep &amp; recovery <b>'), [{ text: 'Sleep & recovery <b>' }]);
});

test('the review as Claude writes it', () => {
  const text = 'A lighter week.\n\n**Nutrition**\n- You logged **7** days.\n- Protein fell.\n\n## Sleep &amp; recovery\n- 438 min.\nSecond line\nof a paragraph.';
  assert.deepEqual(parseReview(text), [
    { type: 'p', parts: [{ text: 'A lighter week.' }] },
    { type: 'h', parts: [{ text: 'Nutrition' }] },
    { type: 'ul', items: [[{ text: 'You logged ' }, { text: '7', bold: true }, { text: ' days.' }], [{ text: 'Protein fell.' }]] },
    { type: 'h', parts: [{ text: 'Sleep & recovery' }] },
    { type: 'ul', items: [[{ text: '438 min.' }]] },
    { type: 'p', parts: [{ text: 'Second line' }, { text: ' ' }, { text: 'of a paragraph.' }] },
  ]);
  assert.deepEqual(parseReview(''), []);
});

test('credit notices for the site owner', () => {
  assert.deepEqual(reviewNotices({}), []);
  assert.match(reviewNotices({ review_status: { billing: true } })[0], /credit is used up/);
  assert.match(reviewNotices({ review_status: { credit_low: true, remaining_usd: 0.3, weeks_left: 3 } })[0], /\$0,30 left, enough for about 3 more/);
  assert.match(reviewNotices({ review_status: { credit_low: true, remaining_usd: 0.1, weeks_left: 1 } })[0], /one more weekly review/);
});
