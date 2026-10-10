// Independent piece-commitment check. The algorithm is copied from the C7 rules
// (1 KiB leaves, pad the leaf count to a power of two, SHA-256 with a tag byte),
// not imported from Go. It must agree with testdata/vectors.json.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { createHash } from 'node:crypto';
import { fileURLToPath } from 'node:url';

const LEAF = 1024;
const TAG_EMPTY = 0x00;
const TAG_LEAF = 0x01;
const TAG_NODE = 0x02;
const EMPTY_LABEL = 'ORAMA_PIECE_EMPTY_V1';

const vectors = JSON.parse(
  readFileSync(fileURLToPath(new URL('./testdata/vectors.json', import.meta.url)), 'utf8'),
);

function sha256(parts) {
  const h = createHash('sha256');
  for (const p of parts) h.update(p);
  return h.digest();
}

function realLeafCount(n) {
  if (n <= 0) return 0;
  return Math.ceil(n / LEAF);
}

function paddedLeafCount(real) {
  if (real === 0) return 0;
  if ((real & (real - 1)) === 0) return real;
  return 1 << (32 - Math.clz32(real - 1));
}

function leafBytes(data, index, real) {
  const buf = Buffer.alloc(LEAF);
  if (index >= real) return buf;
  const start = index * LEAF;
  if (data.length <= start) return buf;
  data.copy(buf, 0, start, Math.min(start + LEAF, data.length));
  return buf;
}

function commit(data) {
  const real = realLeafCount(data.length);
  const padded = paddedLeafCount(real);
  if (padded === 0) {
    return { root: sha256([Buffer.from([TAG_EMPTY]), Buffer.from(EMPTY_LABEL)]), real, padded, levels: [] };
  }
  let level = [];
  for (let i = 0; i < padded; i++) {
    level.push(sha256([Buffer.from([TAG_LEAF]), leafBytes(data, i, real)]));
  }
  const levels = [level];
  while (level.length > 1) {
    const next = [];
    for (let i = 0; i < level.length; i += 2) {
      next.push(sha256([Buffer.from([TAG_NODE]), level[i], level[i + 1]]));
    }
    level = next;
    levels.push(level);
  }
  return { root: level[0], real, padded, levels };
}

function prove(data, index) {
  const built = commit(data);
  if (built.padded === 0) throw new Error('empty');
  const siblings = [];
  let idx = index;
  for (let level = 0; level < built.levels.length - 1; level++) {
    siblings.push(built.levels[level][idx ^ 1]);
    idx = Math.floor(idx / 2);
  }
  return { index, leaf: leafBytes(data, index, built.real), siblings };
}

function verify(commitment, proof) {
  let acc = sha256([Buffer.from([TAG_LEAF]), proof.leaf]);
  let idx = proof.index;
  for (const sib of proof.siblings) {
    acc = idx % 2 === 0
      ? sha256([Buffer.from([TAG_NODE]), acc, sib])
      : sha256([Buffer.from([TAG_NODE]), sib, acc]);
    idx = Math.floor(idx / 2);
  }
  return acc.equals(commitment.root);
}

function leafIndex(seed, real) {
  const digest = sha256([seed]);
  let n = 0n;
  for (const b of digest) n = (n << 8n) + BigInt(b);
  return Number(n % BigInt(real));
}

test('vectors match an independent implementation', () => {
  assert.equal(vectors.leaf_size, LEAF);
  assert.equal(vectors.proof_size_64gib, LEAF + 26 * 32);
  assert.ok(vectors.cases.length > 0);
  for (const tc of vectors.cases) {
    const data = Buffer.from(tc.data_hex, 'hex');
    const built = commit(data);
    assert.equal(built.real, tc.real_leaf_count, tc.name);
    assert.equal(built.padded, tc.padded_leaf_count, tc.name);
    assert.equal(built.root.toString('hex'), tc.root_hex, tc.name);
    for (const vp of tc.proofs) {
      const proof = prove(data, vp.index);
      assert.equal(proof.leaf.toString('hex'), vp.leaf_hex, `${tc.name} leaf ${vp.index}`);
      assert.deepEqual(proof.siblings.map((s) => s.toString('hex')), vp.siblings_hex, `${tc.name} sib ${vp.index}`);
      assert.equal(verify(built, proof), true, `${tc.name} verify ${vp.index}`);
    }
    tc.challenge_seeds_hex.forEach((seedHex, i) => {
      const idx = leafIndex(Buffer.from(seedHex, 'hex'), built.real);
      assert.equal(idx, tc.challenge_indexes[i], `${tc.name} challenge ${i}`);
      assert.ok(idx < built.real);
    });
  }
});

test('padding leaf is never a challenge index', () => {
  const data = Buffer.alloc(3 * LEAF, 0x44);
  const built = commit(data);
  assert.equal(built.real, 3);
  assert.equal(built.padded, 4);
  for (let i = 0; i < 500; i++) {
    const idx = leafIndex(Buffer.from([i & 0xff, (i >> 8) & 0xff, 0x7e]), built.real);
    assert.ok(idx < 3);
  }
});
