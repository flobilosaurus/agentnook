import { randomUUID } from "node:crypto";
import { renameSync, writeFileSync } from "node:fs";
import { pathToFileURL } from "node:url";

export function recordEvent(data, directory = "/run/agentnook-telemetry") {
  // Subagent Stop/SessionEnd must not mark the main conversation idle.
  if (data.agent_id || typeof data.session_id !== "string" || typeof data.hook_event_name !== "string") return;
  // Do not persist stdin: it can contain prompts, tool inputs and credentials.
  const event = { sessionId: data.session_id, event: data.hook_event_name, notification: data.notification_type };
  const file = `${directory}/events/${randomUUID()}.json`;
  writeFileSync(`${file}.tmp`, JSON.stringify(event), { mode: 0o600 });
  renameSync(`${file}.tmp`, file);
}

export async function runHook(input = process.stdin) {
  // Hooks only observe. Always succeed, without stdout or permission decisions.
  try {
    let data = "";
    for await (const chunk of input) {
      data += chunk;
      if (data.length > 1024 * 1024) { input.destroy(); return; }
    }
    recordEvent(JSON.parse(data));
  } catch { /* Missing reporter is harmless. */ }
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) await runHook();
