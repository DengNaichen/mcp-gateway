import test from 'node:test';
import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import {
  BASE_SLOT_API_PORT,
  BASE_SLOT_VITE_PORT,
  ENV_FILE,
  MARKER,
  MAX_SLOT,
  chooseSlot,
  cleanWorktree,
  computeDependencyStamp,
  formatManagedEnv,
  parseManagedEnv,
  setupWorktree,
  slotPorts,
  worktreeIdentity,
} from './worktree-runtime.mjs';

test('worktreeIdentity and slotPorts derive stable non-colliding ports', () => {
  const idA = worktreeIdentity('/tmp/repo/.worktrees/feat-a');
  const idB = worktreeIdentity('/tmp/repo/.worktrees/feat-b');
  assert.match(idA, /^[0-9a-f]{12}$/);
  assert.notEqual(idA, idB);

  const slot = chooseSlot(idA, new Set());
  assert.ok(slot >= 1 && slot <= MAX_SLOT);
  const nextSlot = chooseSlot(idA, new Set([slot]));
  assert.notEqual(nextSlot, slot);
  assert.deepEqual(slotPorts(slot), {
    apiPort: BASE_SLOT_API_PORT + slot,
    vitePort: BASE_SLOT_VITE_PORT + slot,
  });
});

test('chooseSlot fails when all slots are occupied', () => {
  const full = new Set(Array.from({ length: MAX_SLOT }, (_, idx) => idx + 1));
  assert.throws(() => chooseSlot('0123456789ab', full), /no worktree port slot/);
});

test('parseManagedEnv distinguishes managed vs unmanaged files', () => {
  const raw = formatManagedEnv({
    id: 'abcdef012345',
    root: '/tmp/repo/.worktrees/feat-x',
    slot: 4,
  });
  const parsed = parseManagedEnv(raw);
  assert.equal(parsed.managed, true);
  assert.equal(parsed.values.get('GATEWAY_WORKTREE_SLOT'), '4');
  assert.equal(parsed.values.get('GATEWAY_DEV_ADDR'), '127.0.0.1:8804');
  assert.equal(parsed.values.get('GATEWAY_VITE_PORT'), '5184');
  assert.equal(parsed.values.get('VITE_API_PORT'), '8804');

  assert.deepEqual(parseManagedEnv('GATEWAY_DEV_ADDR=127.0.0.1:9999\n'), { managed: false });
});

test('computeDependencyStamp reflects manifest, lockfile, and platform ABI', () => {
  const base = {
    packageJson: '{"name":"web"}',
    packageLock: '{"lockfileVersion":3}',
    platform: 'darwin',
    arch: 'arm64',
    nodeMajor: '22',
  };
  const s1 = computeDependencyStamp(base);
  const s2 = computeDependencyStamp({ ...base, packageLock: '{"lockfileVersion":3,"v":2}' });
  const s3 = computeDependencyStamp({ ...base, arch: 'x64' });
  assert.notEqual(s1, s2);
  assert.notEqual(s1, s3);
  assert.equal(s1, computeDependencyStamp(base));
});

test('setupWorktree and cleanWorktree manage secondary worktrees and protect primary/dirty state', () => {
  const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'gw-wt-test-'));
  try {
    const primary = path.join(tmp, 'repo');
    fs.mkdirSync(primary);
    const git = (...args) =>
      execFileSync('git', ['-C', primary, ...args], {
        encoding: 'utf8',
        stdio: ['ignore', 'pipe', 'pipe'],
      }).trim();
    git('init', '-b', 'main');
    git('config', 'user.email', 'test@example.com');
    git('config', 'user.name', 'Test User');
    fs.writeFileSync(path.join(primary, '.gitignore'), '/.worktrees/\n.env*\n');
    fs.writeFileSync(path.join(primary, 'README.md'), '# test\n');
    git('add', '.gitignore', 'README.md');
    git('commit', '-m', 'init');

    const wt1 = path.join(primary, '.worktrees', 'feat-one');
    const wt2 = path.join(primary, '.worktrees', 'feat-two');
    git('worktree', 'add', '-b', 'feat/one', wt1);
    git('worktree', 'add', '-b', 'feat/two', wt2);

    // Primary setup does not create .env.worktree.
    const primaryRes = setupWorktree(primary, { skipNpm: true });
    assert.equal(primaryRes.runtime, null);
    assert.equal(fs.existsSync(path.join(primary, ENV_FILE)), false);

    // Secondary worktrees get distinct slots and managed .env.worktree.
    const res1 = setupWorktree(wt1, { skipNpm: true });
    const res2 = setupWorktree(wt2, { skipNpm: true });
    assert.ok(res1.runtime && res2.runtime);
    assert.notEqual(res1.runtime.slot, res2.runtime.slot);
    assert.ok(fs.readFileSync(path.join(wt1, ENV_FILE), 'utf8').startsWith(`${MARKER}\n`));

    // Rerun preserves slot.
    const res1Again = setupWorktree(wt1, { skipNpm: true });
    assert.equal(res1Again.runtime.slot, res1.runtime.slot);

    // Unmanaged .env.worktree is never overwritten or deleted.
    fs.writeFileSync(path.join(wt2, ENV_FILE), 'CUSTOM=1\n');
    assert.throws(() => setupWorktree(wt2, { skipNpm: true }), /not managed/);
    assert.throws(() => cleanWorktree(wt2), /not managed/);

    // Primary checkout cannot be cleaned.
    assert.throws(() => cleanWorktree(primary), /primary checkout/);

    // Dirty secondary worktree refuses cleanup; clean succeeds after reset.
    fs.writeFileSync(path.join(wt1, 'dirty.txt'), 'wip\n');
    assert.throws(() => cleanWorktree(wt1), /uncommitted changes/);
    fs.rmSync(path.join(wt1, 'dirty.txt'));
    const cleanRes = cleanWorktree(wt1);
    assert.equal(cleanRes.cleaned, true);
    assert.equal(fs.existsSync(path.join(wt1, ENV_FILE)), false);
  } finally {
    fs.rmSync(tmp, { recursive: true, force: true });
  }
});
