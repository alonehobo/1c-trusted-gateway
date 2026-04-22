import fs from 'node:fs';
import path from 'node:path';
import { spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { handleHookEvent } from './memory-gate.mjs';

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);
const WORKSPACE_ROOT = path.resolve(__dirname, '..');
const DASHBOARD_HOOK_CANDIDATES = [
  process.env.AGENT_DASHBOARD_CLAUDE_HOOK,
  'C:\\Yandex.Disk\\Cursor\\agent-dashboard\\adapters\\claude-code\\hook.mjs',
].filter(Boolean);

function readPayload(rawInput) {
  try {
    return rawInput.trim() ? JSON.parse(rawInput) : {};
  } catch {
    return {};
  }
}

function resolveDashboardHook() {
  return DASHBOARD_HOOK_CANDIDATES.find((candidate) => {
    try {
      return fs.existsSync(candidate);
    } catch {
      return false;
    }
  });
}

function forwardToDashboard(eventType, rawInput) {
  const dashboardHook = resolveDashboardHook();
  if (!dashboardHook) {
    return;
  }

  try {
    spawnSync('node', [dashboardHook, eventType], {
      input: rawInput,
      stdio: 'ignore',
      windowsHide: true,
    });
  } catch {
    // Dashboard forwarding is optional.
  }
}

function main() {
  const eventType = process.argv[2] || 'unknown';
  const rawInput = fs.readFileSync(0, 'utf8');
  const payload = readPayload(rawInput);

  forwardToDashboard(eventType, rawInput);

  const outcome = handleHookEvent(eventType, payload, {
    workspaceRoot: WORKSPACE_ROOT,
  });

  if (outcome.output) {
    process.stderr.write(`${outcome.output}\n`);
  }

  process.exit(outcome.exitCode);
}

main();
