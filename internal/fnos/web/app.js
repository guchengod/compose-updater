const $ = (id) => document.getElementById(id);
const state = {
  config: null,
  dirty: false,
  authorizedPaths: [],
  runtime: { scheduler: {}, runs: [] },
  selectedRunId: null,
  pickerInput: null,
  pickerPath: '',
  pickerSelected: '',
  locations: [],
  currentView: 'runs'
};

function apiURL(name) {
  const base = window.location.pathname.endsWith('/') ? window.location.pathname : `${window.location.pathname}/`;
  return new URL(`api/${name}`, `${window.location.origin}${base}`).toString();
}

async function api(name, options = {}) {
  const response = await fetch(apiURL(name), {
    credentials: 'same-origin',
    ...options,
    headers: { 'Content-Type': 'application/json', ...(options.headers || {}) }
  });
  const payload = await response.json().catch(() => ({}));
  if (!response.ok) throw new Error(payload.error || `请求失败 (${response.status})`);
  return payload;
}

function element(tag, className, text) {
  const node = document.createElement(tag);
  if (className) node.className = className;
  if (text !== undefined) node.textContent = text;
  return node;
}

function icon(name, alt = '') {
  const image = document.createElement('img');
  image.src = `icons/${name}.svg`;
  image.alt = alt;
  return image;
}

function showMessage(text, error = false) {
  const box = $('message');
  box.textContent = text;
  box.classList.toggle('error', error);
  box.hidden = false;
  window.clearTimeout(showMessage.timer);
  showMessage.timer = window.setTimeout(() => { box.hidden = true; }, 6000);
}

function setDirty(value) {
  state.dirty = value;
  $('dirtyText').textContent = value ? '有尚未保存的修改' : '配置已同步';
}

function setView(view) {
  state.currentView = view;
  document.querySelectorAll('.view').forEach((item) => { item.hidden = item.id !== `${view}View`; });
  document.querySelectorAll('.nav-item').forEach((item) => item.classList.toggle('active', item.dataset.view === view));
  $('actionbar').hidden = !['scan', 'policy', 'registry', 'notify'].includes(view);
}

function pathRow(value = '', group = '') {
  const row = element('div', 'path-item');
  const input = document.createElement('input');
  input.value = value;
  input.placeholder = '/vol1/1000/docker';
  input.required = group === 'paths';
  input.setAttribute('aria-label', group === 'skipDirs' ? '跳过目录' : '扫描目录');
  input.addEventListener('input', () => setDirty(true));

  const choose = element('button', 'choose-directory');
  choose.type = 'button';
  choose.setAttribute('aria-label', '选择目录');
  choose.append(icon('folder-open'));
  choose.addEventListener('click', () => openDirectoryPicker(input));

  const remove = element('button', 'remove-path');
  remove.type = 'button';
  remove.setAttribute('aria-label', '移除目录');
  remove.append(icon('x'));
  remove.addEventListener('click', () => { row.remove(); setDirty(true); });
  row.append(input, choose, remove);
  return row;
}

function renderPaths(id, values) {
  const target = $(id);
  target.replaceChildren(...(values || []).map((value) => pathRow(value, id)));
  if (id === 'paths' && !target.children.length) target.append(pathRow('', id));
}

function fill(config, authorizedPaths = state.authorizedPaths) {
  state.config = config;
  state.authorizedPaths = authorizedPaths || [];
  renderPaths('paths', config.paths);
  renderPaths('skipDirs', config.skip_dirs);
  $('depth').value = config.depth;
  $('schedule').value = config.schedule;
  $('timezone').value = config.timezone;
  $('runOnStart').checked = config.run_on_start;
  $('stableOnly').checked = config.stable_only;
  $('registryProxy').value = config.registry_proxy || '';
  $('barkEnabled').checked = config.bark.enabled;
  $('barkEndpoint').value = config.bark.endpoint || '';
  $('barkGroup').value = config.bark.group || '';
  $('deviceKey').value = '';
  $('deviceKeyEnv').value = config.bark.device_key_env || '';
  $('clearDeviceKey').checked = false;
  $('keyState').textContent = config.bark.device_key_set ? '已安全保存密钥；留空不会修改' : '尚未保存 Device Key';
  $('authorizedHint').textContent = authorizedPaths.length ? `当前可访问的数据目录：${authorizedPaths.join('、')}` : '请选择当前系统实际可访问的绝对路径。';
  setDirty(false);
}

function pathValues(id) {
  return [...$(id).querySelectorAll('input')].map((item) => item.value.trim()).filter(Boolean);
}

function collect() {
  return {
    version: 1,
    paths: pathValues('paths'),
    skip_dirs: pathValues('skipDirs'),
    depth: Number($('depth').value),
    schedule: $('schedule').value.trim(),
    timezone: $('timezone').value.trim(),
    run_on_start: $('runOnStart').checked,
    stable_only: $('stableOnly').checked,
    registry_proxy: $('registryProxy').value.trim(),
    bark: {
      enabled: $('barkEnabled').checked,
      endpoint: $('barkEndpoint').value.trim(),
      device_key: $('deviceKey').value.trim(),
      device_key_env: $('deviceKeyEnv').value.trim(),
      group: $('barkGroup').value.trim(),
      clear_device_key: $('clearDeviceKey').checked
    }
  };
}

async function loadConfig() {
  try {
    const payload = await api('config');
    fill(payload.config, payload.authorized_paths);
    if (payload.config_error) showMessage(`现有配置无法读取：${payload.config_error}。请检查后保存以修复。`, true);
  } catch (error) {
    showMessage(error.message, true);
    $('saveButton').disabled = true;
  }
}

function formatDate(value) {
  if (!value) return '—';
  return new Intl.DateTimeFormat('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false }).format(new Date(value));
}

function formatTime(value) {
  if (!value) return '—';
  return new Intl.DateTimeFormat('zh-CN', { hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false }).format(new Date(value));
}

function historyDate(value) {
  const date = new Date(value);
  const today = new Date();
  if (date.toDateString() === today.toDateString()) return '今天';
  const yesterday = new Date(today); yesterday.setDate(today.getDate() - 1);
  if (date.toDateString() === yesterday.toDateString()) return '昨天';
  return new Intl.DateTimeFormat('zh-CN', { month: 'long', day: 'numeric' }).format(date);
}

function runTitle(run) {
  if (run.status === 'running') return '正在检查更新';
  if (run.status === 'failed') return '检查完成，存在失败';
  if (run.projects_updated > 0) return `已更新 ${run.services_updated} 个服务`;
  return '所有镜像均为最新';
}

function statusText(status) {
  return ({ success: '运行成功', running: '正在运行', failed: '运行失败', unchanged: '已是最新', updated: '已更新', config_updated: '配置已更新', skipped: '已跳过', available: '有可用更新' })[status] || '已完成';
}

function renderRunHistory() {
  const list = $('runHistoryList');
  list.replaceChildren();
  const runs = state.runtime.runs || [];
  if (!runs.length) {
    const empty = element('div', 'empty-state');
    const wrap = document.createElement('div'); wrap.append(icon('history'), element('h2', '', '还没有运行记录'), element('p', '', '完成下一轮定时检查后，这里会显示每个项目的真实执行过程。'));
    empty.append(wrap); list.append(empty); return;
  }
  if (!state.selectedRunId || !runs.some((run) => run.id === state.selectedRunId)) state.selectedRunId = runs[0].id;
  let dateLabel = '';
  runs.forEach((run) => {
    const currentLabel = historyDate(run.started_at);
    if (currentLabel !== dateLabel) { list.append(element('div', 'history-date', currentLabel)); dateLabel = currentLabel; }
    const button = element('button', `run-card${run.id === state.selectedRunId ? ' active' : ''}`); button.type = 'button';
    button.addEventListener('click', () => { state.selectedRunId = run.id; renderRuntime(); });
    const dot = element('span', `run-status-dot ${run.status}`);
    const main = element('span', 'run-card-main');
    const line = element('span', 'run-card-line'); line.append(element('strong', '', runTitle(run)), element('time', '', formatTime(run.started_at)));
    main.append(line, element('small', '', `${run.projects_checked || 0} 个项目 · ${run.duration_ms ? `${(run.duration_ms / 1000).toFixed(1)} 秒` : '进行中'}`));
    button.append(dot, main); list.append(button);
  });
}

function metric(label, value, unit = '', iconName = 'list-check') {
  const item = element('div', 'metric');
  const metricIcon = element('span', 'metric-icon'); metricIcon.append(icon(iconName));
  const copy = element('div', 'metric-copy'); copy.append(element('span', '', label), element('strong', '', String(value ?? 0)));
  if (unit) copy.append(element('small', '', unit));
  item.append(metricIcon, copy);
  return item;
}

function eventIcon(status) {
  if (status === 'failed') return 'alert-triangle';
  if (status === 'running') return 'refresh';
  if (status === 'skipped') return 'chevron-right';
  return 'check';
}

function renderRunDetail() {
  const target = $('runDetail'); target.replaceChildren();
  const run = (state.runtime.runs || []).find((item) => item.id === state.selectedRunId);
  if (!run) {
    const empty = element('div', 'empty-state'); const wrap = document.createElement('div');
    wrap.append(icon('list-check'), element('h2', '', '等待首次检查'), element('p', '', '你可以等待计划任务，或点击右上角“立即运行”生成第一条运行记录。'));
    empty.append(wrap); target.append(empty); return;
  }
  const header = element('div', 'detail-header');
  const title = element('div', 'detail-title'); title.append(element('span', '', `运行 #${run.id.slice(-6)}`), element('h2', '', runTitle(run)), element('p', '', `${formatDate(run.started_at)}${run.finished_at ? ` 至 ${formatTime(run.finished_at)}` : ''}`));
  const detailActions = element('div', 'detail-actions');
  const pill = element('span', `status-pill ${run.status}`); pill.append(icon(eventIcon(run.status)), document.createTextNode(statusText(run.status))); detailActions.append(pill);
  if (run.status !== 'running') { const retry = element('button', 'secondary', '重新运行本次'); retry.type = 'button'; retry.addEventListener('click', triggerRun); detailActions.append(retry); }
  header.append(title, detailActions);
  const metrics = element('div', 'metrics'); metrics.append(metric('检查项目', run.projects_checked, '', 'list-check'), metric('更新项目', run.projects_updated, '', 'refresh'), metric('失败项目', run.projects_failed, '', 'alert-triangle'), metric('总耗时', run.duration_ms ? (run.duration_ms / 1000).toFixed(1) : '—', run.duration_ms ? '秒' : '', 'clock'));
  const timelineHeading = element('div', 'timeline-heading'); timelineHeading.append(element('h3', '', '执行时间线'), element('span', '', `${(run.events || []).length} 个事件`));
  const timeline = element('div', 'timeline');
  (run.events || []).forEach((event) => {
    const row = element('div', `timeline-event ${event.status}`);
    const statusIcon = element('span', 'timeline-icon'); statusIcon.append(icon(eventIcon(event.status)));
    const copy = element('div', 'timeline-copy'); copy.append(element('strong', '', event.title), element('p', '', event.detail || ''));
    row.append(statusIcon, copy, element('time', '', formatTime(event.time))); timeline.append(row);
  });
  const details = element('details', 'technical');
  const summary = element('summary', '', '技术详情'); details.append(summary);
  const raw = (run.raw || []).join('\n') || JSON.stringify({ mode: run.mode, projects: run.projects || [] }, null, 2);
  details.append(element('pre', '', raw));
  target.append(header, metrics, timelineHeading, timeline, details);
}

function renderProjects() {
  const target = $('projectOverview'); target.replaceChildren();
  const run = (state.runtime.runs || [])[0];
  if (!run || !(run.projects || []).length) {
    const empty = element('div', 'empty-state'); const wrap = document.createElement('div'); wrap.append(icon('layout-dashboard'), element('h2', '', '暂无项目状态'), element('p', '', '项目完成一次真实检查后，会在这里汇总展示。')); empty.append(wrap); target.append(empty); return;
  }
  run.projects.forEach((project) => {
    const row = element('div', 'project-row');
    const name = document.createElement('div'); name.append(element('strong', '', project.project || '未命名项目'), element('code', '', project.compose_file));
    const projectDetail = project.error || ((project.services || []).length ? `服务：${project.services.join('、')}` : '未发生镜像变更');
    row.append(name, element('span', `project-status ${project.status}`, statusText(project.status)), element('p', '', projectDetail));
    target.append(row);
  });
}

function renderRuntime() {
  renderRunHistory(); renderRunDetail(); renderProjects();
  const active = (state.runtime.runs || [])[0]?.status === 'running';
  $('runNowButton').disabled = active;
  $('runNowButton').lastChild.textContent = active ? '正在运行' : '立即运行';
  const scheduler = state.runtime.scheduler || {};
  $('schedulerState').textContent = scheduler.started ? '运行中' : '等待启动';
  $('schedulerSchedule').textContent = scheduler.schedule || '—';
}

async function loadStatus() {
  try {
    const payload = await api('runtime');
    state.runtime = payload.runtime || { scheduler: {}, runs: [] };
    const badge = $('runningBadge');
    badge.textContent = payload.updater_running ? '更新服务运行中' : '更新服务未运行';
    badge.className = `badge ${payload.updater_running ? '' : 'error'}`;
    $('versionText').textContent = `版本 ${payload.version}`;
    const nextRun = state.runtime.scheduler?.next_run;
    $('nextRunText').textContent = nextRun ? `下次 ${formatDate(nextRun)}` : '等待有效调度';
    renderRuntime();
  } catch (error) {
    $('runningBadge').textContent = '状态不可用'; $('runningBadge').className = 'badge error';
  }
}

function renderLocations() {
  const target = $('directoryLocations'); target.replaceChildren();
  state.locations.forEach((location) => {
    const button = element('button', 'location-button', location.label); button.type = 'button'; button.title = location.path;
    button.classList.toggle('active', state.pickerPath === location.path || state.pickerPath.startsWith(`${location.path}/`));
    button.addEventListener('click', () => loadDirectory(location.path)); target.append(button);
  });
}

async function loadDirectory(directory) {
  const list = $('directoryList'); const error = $('directoryError');
  list.replaceChildren(element('div', 'directory-empty', '正在读取目录…')); error.hidden = true; state.pickerSelected = ''; $('directorySelect').disabled = true;
  try {
    const payload = await api(`directories?path=${encodeURIComponent(directory || '/')}`);
    state.pickerPath = payload.path; state.locations = payload.locations || state.locations; $('directoryCurrent').textContent = payload.path; $('directoryUp').disabled = !payload.parent; $('directoryUp').dataset.parent = payload.parent || ''; renderLocations(); list.replaceChildren();
    if (!payload.directories.length) list.append(element('div', 'directory-empty', '此目录中没有子目录'));
    payload.directories.forEach((item) => {
      const row = element('div', 'directory-entry'); row.tabIndex = 0; row.setAttribute('role', 'button'); row.setAttribute('aria-label', item.name); row.append(icon('folder'), element('span', '', item.name));
      const open = element('button', 'open-folder'); open.type = 'button'; open.setAttribute('aria-label', `打开 ${item.name}`); open.append(icon('chevron-right'));
      open.addEventListener('click', (event) => { event.stopPropagation(); loadDirectory(item.path); }); row.append(open);
      row.addEventListener('click', () => { document.querySelectorAll('.directory-entry').forEach((entry) => entry.classList.remove('selected')); row.classList.add('selected'); state.pickerSelected = item.path; $('directorySelect').disabled = false; });
      row.addEventListener('dblclick', () => loadDirectory(item.path));
      row.addEventListener('keydown', (event) => { if (event.key === 'Enter') loadDirectory(item.path); if (event.key === ' ') { event.preventDefault(); row.click(); } });
      list.append(row);
    });
  } catch (requestError) { list.replaceChildren(); error.textContent = requestError.message; error.hidden = false; }
}

function openDirectoryPicker(input) {
  state.pickerInput = input;
  const initial = input.value.trim() || state.authorizedPaths[0] || '/';
  $('directoryPicker').hidden = false; document.body.classList.add('modal-open'); loadDirectory(initial);
}

function closeDirectoryPicker() {
  $('directoryPicker').hidden = true; document.body.classList.remove('modal-open'); state.pickerInput = null;
}

document.querySelectorAll('.nav-item').forEach((button) => button.addEventListener('click', () => setView(button.dataset.view)));
document.querySelectorAll('[data-add]').forEach((button) => button.addEventListener('click', () => { $(button.dataset.add).append(pathRow('', button.dataset.add)); setDirty(true); }));
document.querySelectorAll('#configForm input').forEach((input) => input.addEventListener('input', () => setDirty(true)));

$('configForm').addEventListener('submit', async (event) => {
  event.preventDefault(); const button = $('saveButton'); button.disabled = true; button.textContent = '正在保存…';
  try {
    const payload = await api('config', { method: 'POST', headers: { 'X-Compose-Updater': 'web' }, body: JSON.stringify(collect()) });
    fill(payload.config); showMessage('配置已保存，更新服务正在重启。'); window.setTimeout(loadStatus, 1200);
  } catch (error) { showMessage(error.message, true); }
  finally { button.disabled = false; button.textContent = '保存并重启'; }
});

$('reloadButton').addEventListener('click', loadConfig);
$('refreshRuntime').addEventListener('click', loadStatus);
async function triggerRun() {
  const button = $('runNowButton'); button.disabled = true;
  try { const payload = await api('run-now', { method: 'POST', headers: { 'X-Compose-Updater': 'web' }, body: '{}' }); showMessage(payload.message); window.setTimeout(loadStatus, 900); }
  catch (error) { showMessage(error.message, true); button.disabled = false; }
}
$('runNowButton').addEventListener('click', triggerRun);
$('directoryUp').addEventListener('click', () => { if ($('directoryUp').dataset.parent) loadDirectory($('directoryUp').dataset.parent); });
$('directorySelect').addEventListener('click', () => { if (state.pickerInput && state.pickerSelected) { state.pickerInput.value = state.pickerSelected; setDirty(true); } closeDirectoryPicker(); });
$('directoryPickerClose').addEventListener('click', closeDirectoryPicker);
$('directoryPickerCancel').addEventListener('click', closeDirectoryPicker);
$('directoryPicker').addEventListener('click', (event) => { if (event.target === $('directoryPicker')) closeDirectoryPicker(); });
document.addEventListener('keydown', (event) => { if (event.key === 'Escape' && !$('directoryPicker').hidden) closeDirectoryPicker(); });
$('testProxy').addEventListener('click', async () => {
  const result = $('proxyResult'); result.hidden = false; result.className = 'inline-result'; result.textContent = '正在通过代理连接 Docker Registry…';
  try { const payload = await api('proxy-test', { method: 'POST', headers: { 'X-Compose-Updater': 'web' }, body: JSON.stringify({ proxy: $('registryProxy').value.trim() }) }); result.textContent = payload.message; }
  catch (error) { result.className = 'inline-result error'; result.textContent = error.message; }
});
window.addEventListener('beforeunload', (event) => { if (state.dirty) { event.preventDefault(); event.returnValue = ''; } });

setView('runs'); loadConfig(); loadStatus(); window.setInterval(loadStatus, 15000);
