// modes.js — Compact and Notify mode UIs (standalone windows, not the main editor)

function makeCopyRow(text) {
  const row = document.createElement('div');
  row.style.cssText = 'display:flex;align-items:center;gap:8px;';
  const code = document.createElement('code');
  code.textContent = text;
  code.style.cssText = 'flex:1;font-size:13px;background:#161b22;border:1px solid #30363d;border-radius:6px;padding:6px 10px;color:#58a6ff;';
  const btn = document.createElement('button');
  btn.textContent = 'Copy';
  btn.style.cssText = 'padding:4px 12px;background:#238636;color:#fff;border:none;border-radius:6px;cursor:pointer;font-size:12px;';
  btn.onclick = () => { navigator.clipboard.writeText(text); btn.textContent = 'Copied!'; setTimeout(() => btn.textContent = 'Copy', 1500); };
  row.appendChild(code);
  row.appendChild(btn);
  return row;
}

function modeShell() {
  document.body.innerHTML = '';
  document.body.style.cssText = 'font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif;background:#0d1117;color:#e6edf3;padding:16px;display:flex;flex-direction:column;height:100vh;overflow:hidden;';
}

export async function initCompactMode() {
  const app = window.go.main.CompactApp;
  modeShell();

  const title = document.createElement('h2');
  title.textContent = '🖼 Surgery Compact';
  title.style.cssText = 'color:#58a6ff;margin-bottom:12px;font-size:16px;flex-shrink:0;';
  document.body.appendChild(title);

  const status = document.createElement('p');
  status.textContent = 'Rendering conversation as images...';
  status.style.cssText = 'color:#8b949e;font-size:13px;margin-bottom:12px;flex-shrink:0;';
  document.body.appendChild(status);

  const contentDiv = document.createElement('div');
  contentDiv.style.cssText = 'flex:1;display:flex;flex-direction:column;overflow:hidden;min-height:0;';
  document.body.appendChild(contentDiv);

  const data = await app.RunCompact();
  if (data.error) {
    status.textContent = 'Error: ' + data.error;
    status.style.color = '#f85149';
    return;
  }
  status.textContent = 'Done!';

  // Resume commands
  const cmdsDiv = document.createElement('div');
  cmdsDiv.style.cssText = 'margin-bottom:12px;display:flex;flex-direction:column;gap:6px;flex-shrink:0;';
  cmdsDiv.appendChild(makeCopyRow(data.resume_slash));
  cmdsDiv.appendChild(makeCopyRow(data.resume_cmd));
  contentDiv.insertBefore(cmdsDiv, contentDiv.firstChild);

  // Report
  const report = document.createElement('pre');
  report.textContent = data.report;
  report.style.cssText = 'font-size:12px;line-height:1.5;white-space:pre-wrap;background:#161b22;border:1px solid #30363d;border-radius:6px;padding:12px;margin-bottom:12px;flex-shrink:0;max-height:150px;overflow-y:auto;';
  contentDiv.appendChild(report);

  // HTML preview
  if (data.html) {
    const label = document.createElement('div');
    label.textContent = 'Chat Preview';
    label.style.cssText = 'font-size:11px;color:#8b949e;margin-bottom:4px;flex-shrink:0;';
    contentDiv.appendChild(label);
    const iframe = document.createElement('iframe');
    iframe.style.cssText = 'flex:1;border:1px solid #30363d;border-radius:6px;background:#fff;min-height:0;';
    iframe.sandbox = 'allow-same-origin';
    contentDiv.appendChild(iframe);
    iframe.srcdoc = data.html;
  }
}

export async function initNotifyMode() {
  const notify = window.go.main.NotifyApp;
  const data = await notify.GetNotification();
  modeShell();
  document.body.style.padding = '24px';

  const title = document.createElement('h2');
  title.textContent = data.title;
  title.style.cssText = 'color:#58a6ff;margin-bottom:16px;font-size:16px;';
  document.body.appendChild(title);

  const sessionMatch = data.message.match(/\/resume\s+(\S+)/);
  const sessionId = sessionMatch ? sessionMatch[1] : null;
  const reportText = data.message.replace(/\nResume command:\n\/resume\s+\S+/g, '');

  if (sessionId) {
    const cmdsDiv = document.createElement('div');
    cmdsDiv.style.cssText = 'margin-bottom:12px;display:flex;flex-direction:column;gap:8px;';
    cmdsDiv.appendChild(makeCopyRow('/resume ' + sessionId));
    cmdsDiv.appendChild(makeCopyRow('claude --resume ' + sessionId));
    document.body.appendChild(cmdsDiv);
  }

  const pre = document.createElement('pre');
  pre.textContent = reportText;
  pre.style.cssText = 'flex:1;overflow:auto;font-size:13px;line-height:1.6;white-space:pre-wrap;background:#161b22;border:1px solid #30363d;border-radius:8px;padding:16px;';
  document.body.appendChild(pre);
}
