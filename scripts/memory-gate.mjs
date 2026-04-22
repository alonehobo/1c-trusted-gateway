import crypto from 'node:crypto';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);
const DEFAULT_WORKSPACE_ROOT = path.resolve(__dirname, '..');
const STATE_DIR = path.join(os.tmpdir(), 'not1c-memory-gate');
const DEFAULT_SESSION_ID = 'manual';
const PROJECT_CHANGE_EXCLUDES = new Set([
  '.git',
  '.memory',
  'node_modules',
]);
const PROJECT_CHANGE_EXCLUDE_PREFIXES = [
  `.claude${path.sep}worktrees`,
];
const CODE_EXTENSIONS = new Set([
  '.js',
  '.mjs',
  '.cjs',
  '.ts',
  '.tsx',
  '.jsx',
  '.py',
  '.go',
  '.java',
  '.kt',
  '.rb',
  '.php',
  '.cs',
  '.cpp',
  '.c',
  '.h',
  '.rs',
  '.swift',
  '.sh',
  '.ps1',
  '.sql',
  '.json',
  '.yaml',
  '.yml',
  '.toml',
  '.xml',
  '.html',
  '.css',
  '.md',
]);

function stripBom(text) {
  return text.replace(/^\uFEFF/, '');
}

function ensureDir(dirPath) {
  fs.mkdirSync(dirPath, { recursive: true });
}

function fileExists(filePath) {
  try {
    fs.accessSync(filePath, fs.constants.F_OK);
    return true;
  } catch {
    return false;
  }
}

function readText(filePath) {
  if (!fileExists(filePath)) {
    return null;
  }

  return stripBom(fs.readFileSync(filePath, 'utf8'));
}

function readJson(filePath) {
  if (!fileExists(filePath)) {
    return null;
  }

  return JSON.parse(readText(filePath));
}

function writeJson(filePath, data) {
  ensureDir(path.dirname(filePath));
  fs.writeFileSync(filePath, `${JSON.stringify(data, null, 2)}\n`, 'utf8');
}

function sha1(value) {
  return crypto.createHash('sha1').update(value).digest('hex');
}

function getStatePath(sessionId, workspaceRoot = DEFAULT_WORKSPACE_ROOT) {
  const key = sha1(`${workspaceRoot}|${sessionId}`);
  return path.join(STATE_DIR, `${key}.json`);
}

function nowIso() {
  return new Date().toISOString();
}

function loadOrCreateState(sessionId, workspaceRoot = DEFAULT_WORKSPACE_ROOT) {
  const statePath = getStatePath(sessionId, workspaceRoot);
  const existing = readJson(statePath);
  if (existing) {
    return { statePath, state: existing };
  }

  const state = {
    sessionId,
    workspaceRoot,
    startedAt: nowIso(),
    lastEventAt: nowIso(),
    eventCount: 0,
  };
  writeJson(statePath, state);
  return { statePath, state };
}

function loadState(sessionId, workspaceRoot = DEFAULT_WORKSPACE_ROOT) {
  const statePath = getStatePath(sessionId, workspaceRoot);
  const existing = readJson(statePath);
  if (!existing) {
    return null;
  }
  return { statePath, state: existing };
}

function saveState(sessionId, workspaceRoot, state) {
  const statePath = getStatePath(sessionId, workspaceRoot);
  writeJson(statePath, state);
}

function deleteState(sessionId, workspaceRoot = DEFAULT_WORKSPACE_ROOT) {
  const statePath = getStatePath(sessionId, workspaceRoot);
  if (fileExists(statePath)) {
    fs.unlinkSync(statePath);
  }
}

function getProjectInfo(workspaceRoot = DEFAULT_WORKSPACE_ROOT) {
  const projectPath = path.join(workspaceRoot, '.memory', 'PROJECT.md');
  const projectText = readText(projectPath);
  if (!projectText) {
    return {
      exists: false,
      initialized: false,
      reason: 'missing_project_md',
    };
  }

  const isTemplate =
    projectText.includes('Шаблонный файл') ||
    projectText.includes('Не инициализировано') ||
    projectText.includes('/prj-init') ||
    projectText.includes('<заполняется');

  return {
    exists: true,
    initialized: !isTemplate,
    reason: isTemplate ? 'template_project' : 'initialized_project',
  };
}

function getActiveInfo(workspaceRoot = DEFAULT_WORKSPACE_ROOT) {
  const activePath = path.join(workspaceRoot, '.memory', 'ACTIVE.md');
  const activeText = readText(activePath);
  if (!activeText) {
    return {
      exists: false,
      activePath,
      changeId: null,
    };
  }

  const match =
    activeText.match(/^\s*Change ID\s*:\s*(.+)$/im) ||
    activeText.match(/^\s*##\s*Change ID\s*$[\r\n]+^\s*(.+)$/im);

  return {
    exists: true,
    activePath,
    changeId: match ? match[1].trim().replace(/^`|`$/g, '') : null,
  };
}

function shouldSkipDir(relativePath, dirName, includeMemory) {
  if (!relativePath) {
    return false;
  }

  if (!includeMemory && PROJECT_CHANGE_EXCLUDES.has(dirName)) {
    return true;
  }

  return PROJECT_CHANGE_EXCLUDE_PREFIXES.some((prefix) => relativePath.startsWith(prefix));
}

function collectRecentFiles(rootDir, sinceMs, options = {}) {
  const includeMemory = options.includeMemory ?? false;
  const relativeBase = options.relativeBase ?? rootDir;
  const results = [];
  const stack = [''];

  while (stack.length > 0) {
    const relativeDir = stack.pop();
    const absoluteDir = path.join(rootDir, relativeDir);
    let entries;
    try {
      entries = fs.readdirSync(absoluteDir, { withFileTypes: true });
    } catch {
      continue;
    }

    for (const entry of entries) {
      const nextRelative = relativeDir ? path.join(relativeDir, entry.name) : entry.name;
      const nextAbsolute = path.join(rootDir, nextRelative);

      if (entry.isSymbolicLink()) {
        continue;
      }

      if (entry.isDirectory()) {
        if (shouldSkipDir(nextRelative, entry.name, includeMemory)) {
          continue;
        }
        stack.push(nextRelative);
        continue;
      }

      if (!entry.isFile()) {
        continue;
      }

      let stats;
      try {
        stats = fs.statSync(nextAbsolute);
      } catch {
        continue;
      }

      if (stats.mtimeMs + 1000 < sinceMs) {
        continue;
      }

      results.push({
        path: path.relative(relativeBase, nextAbsolute).split(path.sep).join('/'),
        absolutePath: nextAbsolute,
        mtimeMs: stats.mtimeMs,
      });
    }
  }

  results.sort((left, right) => left.path.localeCompare(right.path, 'ru'));
  return results;
}

function listProjectChanges(workspaceRoot, startedAt) {
  const sinceMs = Date.parse(startedAt);
  if (Number.isNaN(sinceMs)) {
    return [];
  }

  return collectRecentFiles(workspaceRoot, sinceMs, {
    includeMemory: false,
    relativeBase: workspaceRoot,
  });
}

function listMemoryChanges(workspaceRoot, startedAt) {
  const memoryRoot = path.join(workspaceRoot, '.memory');
  if (!fileExists(memoryRoot)) {
    return [];
  }

  const sinceMs = Date.parse(startedAt);
  if (Number.isNaN(sinceMs)) {
    return [];
  }

  return collectRecentFiles(memoryRoot, sinceMs, {
    includeMemory: true,
    relativeBase: workspaceRoot,
  });
}

function isSignificantChange(projectChanges) {
  if (projectChanges.length >= 3) {
    return true;
  }

  return projectChanges.some((file) => CODE_EXTENSIONS.has(path.extname(file.path).toLowerCase()));
}

function summarizeFiles(files, limit = 5) {
  return files.slice(0, limit).map((file) => file.path);
}

function formatResult(result) {
  const lines = [];
  const prefix = '[memory-gate]';

  if (result.status === 'skip') {
    lines.push(`${prefix} skip: ${result.reason}`);
    return lines.join('\n');
  }

  if (result.status === 'ok') {
    if (result.warnings.length === 0) {
      lines.push(`${prefix} ok`);
    } else {
      lines.push(`${prefix} ok with warnings`);
      for (const warning of result.warnings) {
        lines.push(`${prefix} warning: ${warning}`);
      }
    }
    return lines.join('\n');
  }

  lines.push(`${prefix} fail: ${result.reason}`);
  for (const detail of result.details) {
    lines.push(`${prefix} detail: ${detail}`);
  }
  for (const warning of result.warnings) {
    lines.push(`${prefix} warning: ${warning}`);
  }
  return lines.join('\n');
}

function missingSessionResult(sessionId) {
  return {
    status: 'fail',
    reason: 'missing_session_state',
    exitCode: 4,
    warnings: [],
    details: [
      `No existing state for session '${sessionId}'.`,
      'Run session-start first; otherwise memory-gate cannot validate the full session.',
    ],
  };
}

export function evaluateSession(workspaceRoot = DEFAULT_WORKSPACE_ROOT, startedAt) {
  const projectInfo = getProjectInfo(workspaceRoot);
  if (!projectInfo.exists || !projectInfo.initialized) {
    return {
      status: 'skip',
      reason: projectInfo.reason,
      exitCode: 0,
      warnings: [],
      details: [],
    };
  }

  const projectChanges = listProjectChanges(workspaceRoot, startedAt);
  if (projectChanges.length === 0) {
    return {
      status: 'ok',
      reason: 'no_project_changes',
      exitCode: 0,
      warnings: [],
      details: [],
    };
  }

  const memoryChanges = listMemoryChanges(workspaceRoot, startedAt);
  const activeInfo = getActiveInfo(workspaceRoot);
  const hasChangeContext =
    Boolean(activeInfo.changeId) ||
    memoryChanges.some((file) => file.path.startsWith('.memory/changes/'));
  const warnings = [];

  if (memoryChanges.length === 0) {
    return {
      status: 'fail',
      reason: 'project_changes_without_memory_updates',
      exitCode: 2,
      warnings,
      details: [
        `Changed project files: ${summarizeFiles(projectChanges).join(', ')}`,
        'Update .memory/ACTIVE.md and related memory artifacts before ending the session.',
      ],
    };
  }

  if (isSignificantChange(projectChanges) && !hasChangeContext) {
    return {
      status: 'fail',
      reason: 'significant_changes_without_change_context',
      exitCode: 3,
      warnings,
      details: [
        `Changed project files: ${summarizeFiles(projectChanges).join(', ')}`,
        'No active Change ID or .memory/changes/<change-id>/ activity detected.',
      ],
    };
  }

  const touchedActive = memoryChanges.some((file) => file.path === '.memory/ACTIVE.md');
  const touchedChangelog = memoryChanges.some((file) => file.path === '.memory/CHANGELOG.md');

  if (!touchedActive) {
    warnings.push('Project files changed, but .memory/ACTIVE.md was not updated in this session.');
  }

  if (!touchedChangelog) {
    warnings.push('Project files changed, but .memory/CHANGELOG.md was not updated in this session.');
  }

  return {
    status: 'ok',
    reason: 'memory_protocol_observed',
    exitCode: 0,
    warnings,
    details: [
      `Changed project files: ${summarizeFiles(projectChanges).join(', ')}`,
      `Changed memory files: ${summarizeFiles(memoryChanges).join(', ')}`,
    ],
  };
}

export function handleHookEvent(eventType, payload = {}, options = {}) {
  const workspaceRoot = options.workspaceRoot ?? DEFAULT_WORKSPACE_ROOT;
  const sessionId = payload.session_id || options.sessionId || DEFAULT_SESSION_ID;
  if (eventType === 'session_end' || eventType === 'stop') {
    return closeSession(sessionId, workspaceRoot);
  }

  const { state } = loadOrCreateState(sessionId, workspaceRoot);
  state.lastEventAt = nowIso();
  state.eventCount = (state.eventCount ?? 0) + 1;
  state.lastEventType = eventType;
  state.lastToolName = payload.tool_name || null;
  saveState(sessionId, workspaceRoot, state);
  return {
    exitCode: 0,
    output: '',
    result: null,
  };
}

export function closeSession(sessionId = DEFAULT_SESSION_ID, workspaceRoot = DEFAULT_WORKSPACE_ROOT) {
  const loaded = loadState(sessionId, workspaceRoot);
  if (!loaded) {
    const result = missingSessionResult(sessionId);
    return {
      exitCode: result.exitCode,
      output: formatResult(result),
      result,
    };
  }

  const { state } = loaded;
  const result = evaluateSession(workspaceRoot, state.startedAt);
  deleteState(sessionId, workspaceRoot);
  return {
    exitCode: result.exitCode,
    output: formatResult(result),
    result,
  };
}

function readStdinJson() {
  try {
    const input = fs.readFileSync(0, 'utf8');
    if (!input.trim()) {
      return {};
    }
    return JSON.parse(input);
  } catch {
    return {};
  }
}

function printUsage() {
  process.stdout.write(
    [
      'Usage:',
      '  node scripts/memory-gate.mjs hook <tool_start|tool_end|session_end>',
      '  node scripts/memory-gate.mjs session-start [session-id]',
      '  node scripts/memory-gate.mjs session-end [session-id]',
      '',
    ].join('\n'),
  );
}

function main() {
  const [command, arg] = process.argv.slice(2);
  const workspaceRoot = path.resolve(process.cwd());

  if (!command || command === '--help' || command === '-h') {
    printUsage();
    process.exit(0);
  }

  if (command === 'hook') {
    const payload = readStdinJson();
    const outcome = handleHookEvent(arg || 'unknown', payload, { workspaceRoot });
    if (outcome.output) {
      process.stderr.write(`${outcome.output}\n`);
    }
    process.exit(outcome.exitCode);
  }

  if (command === 'session-start') {
    const sessionId = arg || DEFAULT_SESSION_ID;
    const { state } = loadOrCreateState(sessionId, workspaceRoot);
    state.lastEventAt = nowIso();
    state.lastEventType = 'manual_session_start';
    saveState(sessionId, workspaceRoot, state);
    process.stdout.write(`[memory-gate] session-start: ${sessionId}\n`);
    process.exit(0);
  }

  if (command === 'session-end') {
    const sessionId = arg || DEFAULT_SESSION_ID;
    const outcome = closeSession(sessionId, workspaceRoot);
    process.stderr.write(`${outcome.output}\n`);
    process.exit(outcome.exitCode);
  }

  printUsage();
  process.exit(1);
}

if (process.argv[1] && path.resolve(process.argv[1]) === __filename) {
  main();
}
