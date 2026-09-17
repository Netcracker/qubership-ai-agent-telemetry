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
const { filterVersions } = await import(
  `file://${renovateRoot}/workers/repository/process/lookup/filter.js`
);
const { api: goDirectiveVersioning } = await import(
  `file://${renovateRoot}/modules/versioning/go-mod-directive/index.js`
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

const dependency = {
  ...config,
  manager: 'gomod',
  datasource: 'golang-version',
  depName: 'go',
  packageName: 'go',
  currentValue: '1.20',
  currentVersion: '1.20.0',
  depType: 'golang',
};

const minorUpdate = await applyPackageRules(
  { ...dependency, updateType: 'minor' },
  'lookup',
);
assert.equal(minorUpdate.allowedVersions, '/^\\d+\\.\\d+\\.0$/');
assert.equal(minorUpdate.rangeStrategy, 'bump');
assert.equal(minorUpdate.minimumReleaseAge, '5 years');
assert.notEqual(minorUpdate.enabled, false);
assert.equal(minorUpdate.automerge, true);

const releases = [
  { version: '1.20.0' },
  { version: '1.21.0' },
  { version: '1.21.13' },
  { version: '1.22.0' },
  { version: '1.22.6' },
];
const eligibleReleases = filterVersions(
  {
    ...minorUpdate,
    ignoreDeprecated: true,
    ignoreUnstable: true,
    respectLatest: true,
  },
  dependency.currentVersion,
  '1.22.6',
  releases,
  goDirectiveVersioning,
);
assert.deepEqual(
  eligibleReleases.map(({ version }) => version),
  ['1.21.0', '1.22.0'],
);
assert.equal(
  goDirectiveVersioning.getNewValue({
    currentValue: dependency.currentValue,
    rangeStrategy: minorUpdate.rangeStrategy,
    newVersion: eligibleReleases.at(-1).version,
  }),
  '1.22.0',
);

const patchUpdate = await applyPackageRules(
  { ...dependency, updateType: 'patch' },
  'lookup',
);
assert.equal(patchUpdate.enabled, false);

const toolchainUpdate = await applyPackageRules(
  {
    ...dependency,
    currentValue: '1.26.5',
    currentVersion: '1.26.5',
    depType: 'toolchain',
    updateType: 'patch',
  },
  'lookup',
);
assert.notEqual(toolchainUpdate.enabled, false);
assert.equal(toolchainUpdate.allowedVersions, undefined);
assert.equal(toolchainUpdate.automerge, true);

console.log('Minimum Go version policy fixtures passed');
