// Мини-библиотека графиков для дашборда: чистый SVG/HTML без зависимостей,
// чтобы не расширять CSP внешними CDN. Все функции возвращают разметку,
// рендер — вставкой в innerHTML контейнера.

// Палитра статусов согласована с бейджами списков.
export const STATUS_COLORS = {
  new: '#4f7cf0',
  assigned: '#7b61e8',
  in_progress: '#e8a13c',
  needs_clarification: '#c9971a',
  answer_ready: '#2ba0c9',
  completed: '#1e9e6a',
  returned: '#d97706',
  rejected: '#e5484d',
  closed_no_response: '#8a94a6',
};

export const CHART_COLORS = ['#4f7cf0', '#7b61e8', '#e8a13c', '#1e9e6a', '#e5484d', '#2ba0c9', '#d97706', '#8a94a6'];

// KPI-плитки: [{label, value, hint, tone}] — tone подсвечивает плитку (ok/bad/mut).
export function kpiHTML(items) {
  return '<div class="kpi-grid">' + items.map((k) =>
    `<div class="kpi${k.tone ? ' ' + k.tone : ''}"><span class="kpi-v">${k.value}</span>` +
    `<span class="kpi-l">${k.label}</span>${k.hint ? `<span class="kpi-h">${k.hint}</span>` : ''}</div>`
  ).join('') + '</div>';
}

// Сгруппированные столбики по дням: [{day, created, completed}].
// Подписи дат прореживаются, всплывающая подсказка — нативный <title>.
export function dailyBarsSVG(days) {
  if (!days || !days.length) return '<div class="note">нет данных за период</div>';
  const W = 720, H = 220, padL = 34, padB = 26, padT = 10;
  const iw = W - padL - 8, ih = H - padB - padT;
  const max = Math.max(1, ...days.map((d) => Math.max(d.created, d.completed)));
  const step = iw / days.length;
  const bw = Math.max(2, Math.min(12, step / 2.6));
  const y = (v) => padT + ih - (v / max) * ih;
  // Шкала: 4 горизонтальные линии с целыми значениями.
  const ticks = [0, Math.round(max / 2), max];
  const grid = ticks.map((t) =>
    `<line x1="${padL}" y1="${y(t)}" x2="${W - 8}" y2="${y(t)}" stroke="var(--line)" stroke-width="1"/>` +
    `<text x="${padL - 6}" y="${y(t) + 4}" text-anchor="end" class="ax">${t}</text>`).join('');
  const bars = days.map((d, i) => {
    const cx = padL + step * i + step / 2;
    const h1 = (d.created / max) * ih, h2 = (d.completed / max) * ih;
    const label = (days.length <= 16 || i % Math.ceil(days.length / 10) === 0)
      ? `<text x="${cx}" y="${H - 8}" text-anchor="middle" class="ax">${d.day.slice(8)}.${d.day.slice(5, 7)}</text>` : '';
    return `<g><title>${d.day}: новых ${d.created}, завершено ${d.completed}</title>` +
      (d.created ? `<rect x="${(cx - bw - 1).toFixed(1)}" y="${(y(d.created)).toFixed(1)}" width="${bw.toFixed(1)}" height="${h1.toFixed(1)}" rx="2" fill="${STATUS_COLORS.new}"/>` : '') +
      (d.completed ? `<rect x="${(cx + 1).toFixed(1)}" y="${(y(d.completed)).toFixed(1)}" width="${bw.toFixed(1)}" height="${h2.toFixed(1)}" rx="2" fill="${STATUS_COLORS.completed}"/>` : '') +
      label + '</g>';
  }).join('');
  return `<div class="chart-legend"><span><i style="background:${STATUS_COLORS.new}"></i>новые</span>` +
    `<span><i style="background:${STATUS_COLORS.completed}"></i>завершённые</span></div>` +
    `<svg viewBox="0 0 ${W} ${H}" class="chart-svg" role="img">${grid}${bars}</svg>`;
}

// Кольцевая диаграмма: [{label, count, color}] → SVG-пончик + HTML-легенда.
export function donutHTML(items) {
  const total = items.reduce((s, i) => s + i.count, 0);
  if (!total) return '<div class="note">нет данных за период</div>';
  const R = 15.915; // длина окружности при r=15.915 равна 100 → доли в %
  let off = 25; // старт с 12 часов
  const segs = items.map((i) => {
    const pctVal = (i.count / total) * 100;
    const s = `<circle r="${R}" cx="21" cy="21" fill="none" stroke="${i.color}" stroke-width="6.5" ` +
      `stroke-dasharray="${pctVal.toFixed(2)} ${(100 - pctVal).toFixed(2)}" stroke-dashoffset="${off.toFixed(2)}"><title>${i.label}: ${i.count} (${Math.round(pctVal)}%)</title></circle>`;
    off -= pctVal;
    return s;
  }).join('');
  const legend = items.map((i) =>
    `<span class="lg-row"><i style="background:${i.color}"></i>${i.label}<b>${i.count}</b><em>${Math.round(i.count / total * 100)}%</em></span>`
  ).join('');
  return `<div class="donut-wrap"><svg viewBox="0 0 42 42" class="donut" role="img">` +
    `<circle r="${R}" cx="21" cy="21" fill="none" stroke="var(--line)" stroke-width="6.5"/>${segs}` +
    `<text x="21" y="20" text-anchor="middle" class="donut-c">${total}</text>` +
    `<text x="21" y="27" text-anchor="middle" class="donut-t">всего</text></svg>` +
    `<div class="legend">${legend}</div></div>`;
}

// Горизонтальные полосы: [{label, count, color?}] → HTML-бары с процентом от max.
export function hbarHTML(rows, opts) {
  const max = opts && opts.max ? opts.max : Math.max(1, ...rows.map((r) => r.count));
  if (!rows.length) return '<div class="note">нет данных за период</div>';
  return rows.map((r) => {
    const w = Math.max(1.5, (r.count / max) * 100);
    const color = r.color || CHART_COLORS[0];
    return `<div class="hbar"><span class="hbar-l">${r.label}</span>` +
      `<span class="hbar-track"><i style="width:${w.toFixed(1)}%;background:${color}"></i></span>` +
      `<b>${r.count}</b></div>`;
  }).join('');
}

// Полоса нагрузки специалиста: active из limit, красная — на лимите и выше.
export function loadbarHTML(active, limit) {
  const pct = Math.min(100, limit > 0 ? (active / limit) * 100 : 0);
  const tone = active >= limit ? 'bad' : (active >= limit * 0.75 ? 'warn' : 'ok');
  return `<span class="loadbar ${tone}" title="активных ${active} из лимита ${limit}"><i style="width:${pct.toFixed(0)}%"></i></span>` +
    `<span class="loadbar-n">${active}/${limit}</span>`;
}
