import assert from 'node:assert/strict';
import fs from 'node:fs';

const renovateRoot = process.env.RENOVATE_DIST_DIR ?? '/usr/local/renovate/dist';
const { init: initializeLogger } = await import(
  `file://${renovateRoot}/logger/index.js`
);
await initializeLogger();
const { applyPackageRules } = await import(
  `file://${renovateRoot}/util/package-rules/index.js`
);

const configPath = process.argv[2] ?? 'renovate.json';
const repositoryConfig = JSON.parse(fs.readFileSync(configPath, 'utf8'));
assert.equal(
  repositoryConfig.platformAutomerge,
  true,
  'eligible updates should use GitHub native automerge',
);
const inheritedAutomergeRule = {
  description: 'Simulate an inherited automerge default',
  automerge: true,
};
const packageRules = [
  inheritedAutomergeRule,
  ...(repositoryConfig.packageRules ?? []),
];

async function applyRules(overrides) {
  return applyPackageRules(
    {
      packageRules,
      manager: 'gomod',
      datasource: 'go',
      depName: 'example.org/dependency',
      packageName: 'example.org/dependency',
      currentValue: 'v1.2.3',
      currentVersion: '1.2.3',
      depType: 'require',
      updateType: 'patch',
      ...overrides,
    },
    'lookup',
  );
}

for (const updateType of ['minor', 'patch', 'pin', 'digest', 'pinDigest']) {
  const dependency = await applyRules({ updateType });
  assert.equal(dependency.automerge, true, `${updateType} should automerge`);
  assert.equal(dependency.automergeType, 'pr');
}

for (const manager of ['github-actions', 'renovate-config']) {
  const dependency = await applyRules({ manager });
  assert.equal(dependency.automerge, false, `${manager} should require review`);
}

const preOneDependency = await applyRules({
  currentValue: 'v0.9.0',
  currentVersion: '0.9.0',
});
assert.equal(preOneDependency.automerge, false);

const majorDependency = await applyRules({ updateType: 'major' });
assert.equal(majorDependency.automerge, false);

const minimumGoDependency = await applyRules({
  datasource: 'golang-version',
  depName: 'go',
  packageName: 'go',
  currentValue: '1.20',
  currentVersion: '1.20.0',
  depType: 'golang',
  updateType: 'minor',
});
assert.equal(minimumGoDependency.automerge, false);

const vulnerabilityAlert = await applyRules({ isVulnerabilityAlert: true });
assert.equal(vulnerabilityAlert.automerge, false);

console.log('Renovate automerge policy fixtures passed');
