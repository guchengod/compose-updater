const $ = (id) => document.getElementById(id);
const state = { config: null, dirty: false };

function apiURL(name) {
  const base = window.location.pathname.endsWith('/') ? window.location.pathname : `${window.location.pathname}/`;
  return new URL(`api/${name}`, `${window.location.origin}${base}`).toString();
}

async function api(name, options = {}) {
  const response = await fetch(apiURL(name), { credentials: 'same-origin', ...options, headers: { 'Content-Type': 'application/json', ...(options.headers || {}) } });
  const payload = await response.json().catch(() => ({}));
  if (!response.ok) throw new Error(payload.error || `请求失败 (${response.status})`);
  return payload;
}

function showMessage(text, error = false) {
  const box = $('message'); box.textContent = text; box.classList.toggle('error', error); box.hidden = false;
  window.clearTimeout(showMessage.timer); showMessage.timer = window.setTimeout(() => { box.hidden = true; }, 6000);
}

function setDirty(value) {
  state.dirty = value; $('dirtyText').textContent = value ? '有尚未保存的修改' : '配置已同步';
}

function pathRow(value = '') {
  const row = document.createElement('div'); row.className = 'path-item';
  const input = document.createElement('input'); input.value = value; input.placeholder = '/vol1/docker'; input.required = row.parentElement?.id === 'paths';
  const remove = document.createElement('button'); remove.type = 'button'; remove.title = '移除'; remove.setAttribute('aria-label', '移除目录'); remove.textContent = '×';
  remove.addEventListener('click', () => { row.remove(); setDirty(true); });
  input.addEventListener('input', () => setDirty(true)); row.append(input, remove); return row;
}

function renderPaths(id, values) {
  const target = $(id); target.replaceChildren(...(values || []).map(pathRow));
  if (id === 'paths' && !target.children.length) target.append(pathRow());
}

function fill(config, authorizedPaths = []) {
  state.config = config; renderPaths('paths', config.paths); renderPaths('skipDirs', config.skip_dirs);
  $('depth').value = config.depth; $('schedule').value = config.schedule; $('timezone').value = config.timezone;
  $('runOnStart').checked = config.run_on_start; $('stableOnly').checked = config.stable_only; $('registryProxy').value = config.registry_proxy || '';
  $('barkEnabled').checked = config.bark.enabled; $('barkEndpoint').value = config.bark.endpoint || ''; $('barkGroup').value = config.bark.group || '';
  $('deviceKey').value = ''; $('deviceKeyEnv').value = config.bark.device_key_env || ''; $('clearDeviceKey').checked = false;
  $('keyState').textContent = config.bark.device_key_set ? '已安全保存密钥；留空不会修改' : '尚未保存 Device Key';
  $('authorizedHint').textContent = authorizedPaths.length ? `飞牛授权的数据目录：${authorizedPaths.join('、')}` : '请填写飞牛中实际可访问的绝对路径。';
  setDirty(false);
}

function pathValues(id) { return [...$(id).querySelectorAll('input')].map((item) => item.value.trim()).filter(Boolean); }
function collect() {
  return { version: 1, paths: pathValues('paths'), skip_dirs: pathValues('skipDirs'), depth: Number($('depth').value), schedule: $('schedule').value.trim(), timezone: $('timezone').value.trim(), run_on_start: $('runOnStart').checked, stable_only: $('stableOnly').checked, registry_proxy: $('registryProxy').value.trim(), bark: { enabled: $('barkEnabled').checked, endpoint: $('barkEndpoint').value.trim(), device_key: $('deviceKey').value.trim(), device_key_env: $('deviceKeyEnv').value.trim(), group: $('barkGroup').value.trim(), clear_device_key: $('clearDeviceKey').checked } };
}

async function loadConfig() {
  try { const payload = await api('config'); fill(payload.config, payload.authorized_paths); if (payload.config_error) showMessage(`现有配置无法读取：${payload.config_error}。请检查后保存以修复。`, true); }
  catch (error) { showMessage(error.message, true); $('saveButton').disabled = true; }
}

async function loadStatus() {
  try {
    const status = await api('status'); const badge = $('runningBadge');
    badge.textContent = status.updater_running ? '更新服务运行中' : '更新服务未运行'; badge.className = `badge ${status.updater_running ? '' : 'error'}`;
    $('versionText').textContent = `版本 ${status.version}`; $('nextRunText').textContent = status.next_run ? `下次 ${new Date(status.next_run).toLocaleString('zh-CN')}` : '等待有效调度';
    if (status.config_error) showMessage(status.config_error, true);
  } catch (error) { $('runningBadge').textContent = '状态不可用'; $('runningBadge').className = 'badge error'; }
}

document.querySelectorAll('[data-add]').forEach((button) => button.addEventListener('click', () => { $(button.dataset.add).append(pathRow()); setDirty(true); }));
document.querySelectorAll('input').forEach((input) => input.addEventListener('input', () => setDirty(true)));
$('configForm').addEventListener('submit', async (event) => {
  event.preventDefault(); const button = $('saveButton'); button.disabled = true; button.textContent = '正在保存…';
  try { const payload = await api('config', { method: 'POST', headers: { 'X-Compose-Updater': 'web' }, body: JSON.stringify(collect()) }); fill(payload.config); showMessage('配置已保存，更新服务正在重启。'); window.setTimeout(loadStatus, 1200); }
  catch (error) { showMessage(error.message, true); }
  finally { button.disabled = false; button.textContent = '保存并重启'; }
});
$('reloadButton').addEventListener('click', loadConfig);
$('testProxy').addEventListener('click', async () => {
  const result = $('proxyResult'); result.hidden = false; result.className = 'inline-result'; result.textContent = '正在连接 Docker Registry…';
  try { const payload = await api('proxy-test', { method: 'POST', headers: { 'X-Compose-Updater': 'web' }, body: JSON.stringify({ proxy: $('registryProxy').value.trim() }) }); result.textContent = `连接成功：${payload.status}`; }
  catch (error) { result.className = 'inline-result error'; result.textContent = error.message; }
});
document.querySelectorAll('.sidebar a').forEach((link) => link.addEventListener('click', () => { document.querySelectorAll('.sidebar a').forEach((item) => item.classList.remove('active')); link.classList.add('active'); }));
window.addEventListener('beforeunload', (event) => { if (state.dirty) { event.preventDefault(); event.returnValue = ''; } });

loadConfig(); loadStatus(); window.setInterval(loadStatus, 30000);
