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
const { resolveConfigPresets } = await import(
  `file://${renovateRoot}/config/presets/index.js`
);
const { getConfig } = await import(`file://${renovateRoot}/config/defaults.js`);
const { mergeChildConfig } = await import(`file://${renovateRoot}/config/utils.js`);
const { add: addHostRule } = await import(`file://${renovateRoot}/util/host-rules.js`);
if (process.env.GITHUB_COM_TOKEN) {
  addHostRule({
    hostType: 'github',
    matchHost: 'api.github.com',
    token: process.env.GITHUB_COM_TOKEN,
  });
}

const configPath = process.argv[2] ?? 'renovate.json';
const repositoryConfig = JSON.parse(fs.readFileSync(configPath, 'utf8'));
const { config: resolvedConfig } = await resolveConfigPresets(repositoryConfig);
const config = mergeChildConfig(getConfig(), resolvedConfig);
assert.equal(
  config.platformAutomerge,
  true,
  'eligible updates should use GitHub native automerge',
);
async function applyRules(overrides) {
  return applyPackageRules(
    {
      ...config,
      ...(overrides.isVulnerabilityAlert ? config.vulnerabilityAlerts : {}),
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

const dependencies = [
  { manager: 'gomod', datasource: 'go' },
  { manager: 'github-actions', datasource: 'github-tags' },
  { manager: 'renovate-config', datasource: 'github-tags' },
  { manager: 'dockerfile', datasource: 'docker' },
  { manager: 'docker-compose', datasource: 'docker' },
  { manager: 'custom.regex', datasource: 'github-releases' },
  { manager: 'maven', datasource: 'maven' },
  { manager: 'gomod', datasource: 'golang-version', depType: 'toolchain' },
];
const automaticUpdateTypes = ['minor', 'patch', 'pin', 'digest', 'pinDigest'];
for (const fixture of dependencies) {
  for (const updateType of [...automaticUpdateTypes, 'major', 'lockFileMaintenance']) {
    for (const currentVersion of ['0.9.0', '1.2.3']) {
      for (const isVulnerabilityAlert of [false, true]) {
        const dependency = await applyRules({
          ...fixture,
          updateType,
          currentVersion,
          currentValue: currentVersion,
          isVulnerabilityAlert,
        });
        assert.equal(
          dependency.automerge,
          automaticUpdateTypes.includes(updateType),
          `${fixture.manager} ${fixture.datasource} ${currentVersion} ${updateType}, vulnerability: ${isVulnerabilityAlert}`,
        );
        assert.equal(dependency.automergeType, 'pr');
      }
    }
  }
}

const minimumGoDependency = await applyRules({
  datasource: 'golang-version',
  depName: 'go',
  packageName: 'go',
  currentValue: '1.20',
  currentVersion: '1.20.0',
  depType: 'golang',
  updateType: 'minor',
});
assert.equal(minimumGoDependency.automerge, true);
assert.equal(minimumGoDependency.minimumReleaseAge, '5 years');

console.log('Renovate automerge policy fixtures passed');
